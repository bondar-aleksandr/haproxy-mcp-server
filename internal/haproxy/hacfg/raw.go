package hacfg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Source is the raw text of a configuration.
type Source struct {
	Text     string
	Modified time.Time // the latest modification time of the files
	Size     int64     // the total size of the files
	Files    []string
}

// files returns the files making up the configuration at path: the file itself, or the *.cfg files
// of a directory in name order (like "haproxy -f <dir>").
func files(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	list, err := filepath.Glob(filepath.Join(path, "*.cfg"))
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no *.cfg file in %s", path)
	}
	sort.Strings(list)
	return list, nil
}

// Stat returns the latest modification time and the total size of the configuration files at path,
// to tell whether they changed.
func Stat(path string) (modified time.Time, size int64, err error) {
	list, err := files(path)
	if err != nil {
		return time.Time{}, 0, err
	}
	for _, f := range list {
		info, err := os.Stat(f)
		if err != nil {
			return time.Time{}, 0, err
		}
		if info.ModTime().After(modified) {
			modified = info.ModTime()
		}
		size += info.Size()
	}
	return modified, size, nil
}

// ReadSource reads the configuration at path as it is. The files of a directory are joined, each one
// preceded by a "# ==> <name> <==" comment line so that it can be told where a line comes from.
func ReadSource(path string) (*Source, error) {
	list, err := files(path)
	if err != nil {
		return nil, err
	}
	src := &Source{Files: list}
	var text strings.Builder
	for _, f := range list {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		if info.ModTime().After(src.Modified) {
			src.Modified = info.ModTime()
		}
		src.Size += int64(len(data))
		if len(list) > 1 || list[0] != path {
			fmt.Fprintf(&text, "# ==> %s <==\n", filepath.Base(f))
		}
		text.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			text.WriteByte('\n')
		}
	}
	src.Text = text.String()
	return src, nil
}

// SectionRef locates a section in the text of a configuration.
type SectionRef struct {
	Header    string `json:"section"`    // "frontend web_FE", "global", "defaults"
	FirstLine int    `json:"first_line"` // 1-based, includes the comment lines right above the header
	LastLine  int    `json:"last_line"`
}

// Sections locates the sections of a configuration. A section starts at its header line, or at the
// comment lines right above it, and runs up to the start of the next one; trailing blank lines are
// not part of it. Lines before the first section belong to none.
func Sections(text string) []SectionRef {
	lines := strings.Split(text, "\n")
	var headers []int
	for i, l := range lines {
		if toks := tokenize(l); len(toks) > 0 && sectionKeywords[toks[0]] {
			headers = append(headers, i)
		}
	}
	starts := make([]int, len(headers))
	for k, h := range headers {
		start := h
		for start > 0 && (k == 0 || start-1 > headers[k-1]) && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "#") {
			start--
		}
		starts[k] = start
	}
	refs := make([]SectionRef, len(headers))
	for k, h := range headers {
		end := len(lines) - 1
		if k+1 < len(headers) {
			end = starts[k+1] - 1
		}
		for end > h && strings.TrimSpace(lines[end]) == "" {
			end--
		}
		toks := tokenize(lines[h])
		header := toks[0]
		if len(toks) > 1 {
			header += " " + toks[1]
		}
		refs[k] = SectionRef{Header: header, FirstLine: starts[k] + 1, LastLine: end + 1}
	}
	return refs
}

// Filter returns the sections whose header (e.g. "frontend web_FE") contains substr, as they are
// written, separated by a blank line, with where they are. An empty substr returns the whole text.
// The second result lists every section header when nothing matches.
func Filter(text, substr string) (string, []SectionRef, []string) {
	if substr == "" {
		return text, nil, nil
	}
	lines := strings.Split(text, "\n")
	var out []string
	var matched []SectionRef
	var all []string
	for _, ref := range Sections(text) {
		all = append(all, ref.Header)
		if strings.Contains(ref.Header, substr) {
			matched = append(matched, ref)
			out = append(out, strings.Join(lines[ref.FirstLine-1:ref.LastLine], "\n"))
		}
	}
	if len(matched) == 0 {
		return "", nil, all
	}
	return strings.Join(out, "\n\n") + "\n", matched, nil
}

const masked = "<redacted>"

// A header whose name suggests a credential.
const secretName = `[\w-]*(?:auth|token|key|secret|passw|signature|credential)[\w-]*`

var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	// stats auth user:password
	{regexp.MustCompile(`(?i)(\bstats\s+auth\s+[^\s:]+:)(\S+)`), `${1}` + masked},
	// userlist: user NAME password HASH / insecure-password PASSWORD; also "... password X" on a socket
	{regexp.MustCompile(`(?i)(\b(?:insecure-password|password)\s+)(\S+)`), `${1}` + masked},
	// setenv NAME VALUE / presetenv NAME VALUE (values are often secrets)
	{regexp.MustCompile(`(?i)(^\s*(?:setenv|presetenv)\s+\S+\s+)(\S.*?)(\s*(?:#.*)?)$`), `${1}` + masked + `${3}`},
	// set-header / add-header / http-check send hdr NAME VALUE where NAME looks like a credential
	{regexp.MustCompile(`(?i)(\b(?:set-header|add-header|replace-header|hdr)\s+"?` + secretName + `"?\s+)("[^"]*"|'[^']*'|\S+)`), `${1}` + masked},
	// Basic / Bearer credentials
	{regexp.MustCompile(`(?i)(\b(?:basic|bearer)\s+)[A-Za-z0-9+/=._~-]{6,}`), `${1}` + masked},
	// scheme://user:password@host
	{regexp.MustCompile(`(://[^\s:/@]+:)[^\s@/]+(@)`), `${1}` + masked + `${2}`},
	// dynamic-cookie-key SECRET
	{regexp.MustCompile(`(?i)(\bdynamic-cookie-key\s+)(\S+)`), `${1}` + masked},
	// the value matched by an ACL on a credential-like header: req.hdr(x-api-key) -m str SECRET
	{regexp.MustCompile(`(?i)(\b(?:req\.|res\.|)hdr(?:_[a-z]+)?\(\s*` + secretName + `\s*\)\s*(?:-[a-z]+\s+)*(?:(?:str|beg|end|sub|reg|dir|dom|int|bin|found|len|ip|bool)\s+)*)([^\s}#][^}#]*?)(\s*(?:\}|#|\b(?:if|unless|or|and)\b|\|\||&&|$))`), `${1}` + masked + `${3}`},
}

// Redact masks the values that look like credentials in the text of a configuration: stats auth
// passwords, userlist passwords, setenv values, credential-like headers and the values ACLs match
// on them, Basic/Bearer tokens, passwords in URLs. Commented-out lines are treated like the others,
// since a commented secret is still a secret. It is a best-effort filter on patterns, not a
// guarantee: a secret written some other way is not recognized. It returns the number of values
// masked.
func Redact(text string) (string, int) {
	n := 0
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, r := range redactions {
			if found := r.re.FindAllStringIndex(line, -1); len(found) > 0 {
				n += len(found)
				line = r.re.ReplaceAllString(line, r.repl)
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n"), n
}
