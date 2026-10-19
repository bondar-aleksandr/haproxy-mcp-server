package hacfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  stats auth admin:Sup3r$ecret", "  stats auth admin:<redacted>"},
		{"  stats auth admin:pw # the ui", "  stats auth admin:<redacted> # the ui"},
		{"# stats auth admin:oldpass", "# stats auth admin:<redacted>"}, // commented-out secrets too
		{"    user bob insecure-password hunter2", "    user bob insecure-password <redacted>"},
		{"    user bob password $5$abc$def", "    user bob password <redacted>"},
		{"        setenv ACCOUNT_THUMBPRINT 'goWiIj5q6cl4BYioPo3jePSHXVh08i'", "        setenv ACCOUNT_THUMBPRINT <redacted>"},
		{"presetenv TOKEN abc  # note", "presetenv TOKEN <redacted>  # note"},
		{"  http-request set-header X-Api-Key abc123", "  http-request set-header X-Api-Key <redacted>"},
		{`  http-request set-header Authorization "Bearer abcdef123456"`, "  http-request set-header Authorization <redacted>"},
		{`  http-check send meth GET uri /hc hdr Authorization "Basic dXNlcjpwYXNz"`, `  http-check send meth GET uri /hc hdr Authorization <redacted>`},
		{"  acl ok req.hdr(x-api-key) -m str s3cr3t-value", "  acl ok req.hdr(x-api-key) -m str <redacted>"},
		{"  http-request deny unless { req.hdr(X-Token) -m str abc } or is_admin", "  http-request deny unless { req.hdr(X-Token) -m str <redacted> } or is_admin"},
		{"  use_backend b if { hdr(authorization) -m str Bearer abcdefgh1234 } !x", "  use_backend b if { hdr(authorization) -m str <redacted> } !x"},
		{"  server s1 10.0.0.1:80 check", "  server s1 10.0.0.1:80 check"}, // nothing to hide
		{"  http-request set-header X-Forwarded-Proto https if { ssl_fc }", "  http-request set-header X-Forwarded-Proto https if { ssl_fc }"},
		{"  acl h hdr(host) -i app.example.com", "  acl h hdr(host) -i app.example.com"}, // hostnames are not credentials
		{"  http-request redirect location https://u:pw123@example.com/", "  http-request redirect location https://u:<redacted>@example.com/"},
		{"  dynamic-cookie-key MY_SECRET_KEY", "  dynamic-cookie-key <redacted>"},
	}
	for _, c := range cases {
		got, n := Redact(c.in)
		if got != c.want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if (got != c.in) != (n > 0) {
			t.Errorf("Redact(%q): count %d does not match the change", c.in, n)
		}
	}
	// Idempotent, and several lines at once.
	text := "stats auth a:b\nserver s 1.1.1.1:80\nsetenv X y\n"
	out, n := Redact(text)
	again, _ := Redact(out)
	if n != 2 || out != "stats auth a:<redacted>\nserver s 1.1.1.1:80\nsetenv X <redacted>\n" || again != out {
		t.Errorf("multi-line: %q (%d), again %q", out, n, again)
	}
}

const layout = `# global comment

global
    log /dev/log local0

defaults
    mode http

#UI
frontend stats
  bind :8404

# sites
frontend web_FE
  bind :80
  use_backend app_BE if x

# commented out, after web_FE
#backend old_BE

# app
backend app_BE
  server a 10.0.0.1:80
`

func TestSectionsAndFilter(t *testing.T) {
	refs := Sections(layout)
	got := []string{}
	for _, r := range refs {
		got = append(got, r.Header)
	}
	if strings.Join(got, "|") != "global|defaults|frontend stats|frontend web_FE|backend app_BE" {
		t.Fatalf("sections: %v", got)
	}
	// The comment right above a header belongs to its section; the line numbers are 1-based.
	byHeader := map[string]SectionRef{}
	for _, r := range refs {
		byHeader[r.Header] = r
	}
	if r := byHeader["frontend stats"]; r.FirstLine != 9 || r.LastLine != 11 {
		t.Errorf("frontend stats: %+v", r)
	}
	if r := byHeader["frontend web_FE"]; r.FirstLine != 13 || r.LastLine != 19 {
		t.Errorf("frontend web_FE: %+v", r) // includes "# sites" above and the commented-out block below
	}

	out, matched, _ := Filter(layout, "web_FE")
	want := "# sites\nfrontend web_FE\n  bind :80\n  use_backend app_BE if x\n\n# commented out, after web_FE\n#backend old_BE\n"
	if out != want || len(matched) != 1 {
		t.Errorf("filter web_FE:\n got %q\nwant %q", out, want)
	}

	// Several matches are separated by a blank line; the filter matches frontend and backend names alike.
	out, matched, _ = Filter(layout, "_")
	if len(matched) != 2 || !strings.Contains(out, "frontend web_FE") || !strings.Contains(out, "# app\nbackend app_BE\n  server a 10.0.0.1:80\n") {
		t.Errorf("filter _BE: %q %+v", out, matched)
	}

	// Matching is on the header, not on the body: "app_BE" in a use_backend line does not select web_FE.
	if _, matched, _ := Filter(layout, "app_BE"); len(matched) != 1 || matched[0].Header != "backend app_BE" {
		t.Errorf("filter app_BE: %+v", matched)
	}

	// No match: the whole text is not returned, the known headers are.
	if out, matched, all := Filter(layout, "nope"); out != "" || matched != nil || len(all) != 5 {
		t.Errorf("no match: %q %v %v", out, matched, all)
	}

	// No filter: the text as it is.
	if out, _, _ := Filter(layout, ""); out != layout {
		t.Error("an empty filter must return the text unchanged")
	}
}

func TestReadSource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "20-be.cfg"), []byte("backend b\n  server s 1.1.1.1:80"), 0o600) // no trailing newline
	os.WriteFile(filepath.Join(dir, "10-fe.cfg"), []byte("frontend a\n  bind :80\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600)
	src, err := ReadSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "# ==> 10-fe.cfg <==\nfrontend a\n  bind :80\n# ==> 20-be.cfg <==\nbackend b\n  server s 1.1.1.1:80\n"
	if src.Text != want || len(src.Files) != 2 {
		t.Errorf("directory:\n got %q\nwant %q", src.Text, want)
	}

	// A single file is returned as it is, without a marker.
	one := filepath.Join(t.TempDir(), "haproxy.cfg")
	os.WriteFile(one, []byte("global\n"), 0o600)
	if src, err := ReadSource(one); err != nil || src.Text != "global\n" {
		t.Errorf("file: %q, %v", src, err)
	}

	// Stat follows the files inside a directory, not just the directory.
	m1, s1, _ := Stat(dir)
	later := m1.Add(time.Hour)
	os.Chtimes(filepath.Join(dir, "10-fe.cfg"), later, later)
	if m2, _, _ := Stat(dir); !m2.Equal(later) || s1 == 0 {
		t.Errorf("Stat should report the latest file mtime: %v vs %v", m2, later)
	}
}
