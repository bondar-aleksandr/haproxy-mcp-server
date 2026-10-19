// Package hacfg extracts the topology (frontends, their binds and the backends they route to) from
// an HAProxy configuration file. It is a reader for the few directives that matter here, not a
// validator of the configuration language.
package hacfg

import (
	"strings"
)

// Config is the topology described by a configuration.
type Config struct {
	Frontends []Frontend // "frontend" and "listen" sections, in file order
	Backends  []string   // "backend" and "listen" sections, in file order
}

// Frontend is a "frontend" or "listen" section.
type Frontend struct {
	Name           string
	Listen         bool // a "listen" section: also its own backend
	Mode           string
	Binds          []Bind
	DefaultBackend string
	Routes         []Route // use_backend rules, in order
}

// Bind is one address a frontend listens on.
type Bind struct {
	Raw      string // as written, e.g. "*:443"
	Protocol string // "ipv4@" style prefix or the derived family: ipv4, ipv6, unix, quic4, ...
	Address  string // IP address, socket path; empty for "any"
	Port     string // port or port range; empty for sockets
	Wildcard bool   // listens on all addresses (empty, "*", 0.0.0.0, ::)
	SSL      bool
}

// Route is a use_backend rule.
type Route struct {
	Backend   string
	Dynamic   bool              // the backend name is a log-format expression, e.g. %[req.hdr(host),map(...)]
	Condition string            // "if host_app", "unless is_admin", or "" when unconditional
	ACLs      map[string]string // definitions of the named ACLs used in the condition
}

// Load reads and parses the configuration at path: a file, or a directory of *.cfg files read in
// name order (like "haproxy -f <dir>").
func Load(path string) (*Config, error) {
	src, err := ReadSource(path)
	if err != nil {
		return nil, err
	}
	return Parse(src.Text), nil
}

var sectionKeywords = map[string]bool{
	"global": true, "defaults": true, "frontend": true, "backend": true, "listen": true,
	"userlist": true, "resolvers": true, "peers": true, "mailers": true, "cache": true,
	"program": true, "http-errors": true, "ring": true, "log-forward": true, "fcgi-app": true,
	"crt-store": true, "traces": true, "acme": true,
}

// proxy collects the directives of the section being read.
type proxy struct {
	kind, name     string
	from           string // "from <defaults>"
	mode           string
	defaultBackend string
	binds          []Bind
	routes         []Route
	acls           map[string][]string
}

// Parse reads the text of a configuration.
func Parse(text string) *Config {
	cfg := &Config{}
	named := map[string]*proxy{} // named defaults sections
	var lastDefaults *proxy
	var cur *proxy

	finish := func() {
		if cur == nil {
			return
		}
		defer func() { cur = nil }()
		switch cur.kind {
		case "defaults":
			// Only unnamed defaults apply implicitly (to the sections that follow); named ones need "from".
			if cur.name == "" {
				lastDefaults = cur
			} else {
				named[cur.name] = cur
			}
		case "backend":
			cfg.Backends = append(cfg.Backends, cur.name)
		case "frontend", "listen":
			cfg.Frontends = append(cfg.Frontends, cur.frontend(lastDefaults, named))
			if cur.kind == "listen" {
				cfg.Backends = append(cfg.Backends, cur.name)
			}
		}
	}

	for _, line := range strings.Split(text, "\n") {
		toks := tokenize(line)
		if len(toks) == 0 {
			continue
		}
		if sectionKeywords[toks[0]] {
			finish()
			cur = &proxy{kind: toks[0], acls: map[string][]string{}}
			if len(toks) > 1 {
				cur.name = toks[1]
			}
			for i := 2; i+1 < len(toks); i++ {
				if toks[i] == "from" {
					cur.from = toks[i+1]
				}
			}
			continue
		}
		if cur == nil {
			continue
		}
		args := toks[1:]
		switch toks[0] {
		case "mode":
			if len(args) > 0 {
				cur.mode = args[0]
			}
		case "default_backend":
			if len(args) > 0 {
				cur.defaultBackend = args[0]
			}
		case "bind":
			if len(args) > 0 {
				cur.binds = append(cur.binds, parseBind(args)...)
			}
		case "acl":
			if len(args) > 1 {
				cur.acls[args[0]] = append(cur.acls[args[0]], strings.Join(args[1:], " "))
			}
		case "use_backend":
			if len(args) > 0 {
				cur.routes = append(cur.routes, parseRoute(args))
			}
		}
	}
	finish()
	return cfg
}

// frontend builds the Frontend of a "frontend"/"listen" section, applying its defaults section.
func (p *proxy) frontend(lastDefaults *proxy, named map[string]*proxy) Frontend {
	defaults := lastDefaults
	if p.from != "" {
		defaults = named[p.from]
	}
	f := Frontend{
		Name: p.name, Listen: p.kind == "listen", Mode: p.mode, Binds: p.binds,
		DefaultBackend: p.defaultBackend, Routes: p.routes,
	}
	if defaults != nil {
		if f.Mode == "" {
			f.Mode = defaults.mode
		}
		if f.DefaultBackend == "" {
			f.DefaultBackend = defaults.defaultBackend
		}
	}
	for i := range f.Routes {
		f.Routes[i].ACLs = p.usedACLs(f.Routes[i].Condition, defaults)
	}
	return f
}

// usedACLs returns the definitions of the named ACLs mentioned in a condition.
func (p *proxy) usedACLs(condition string, defaults *proxy) map[string]string {
	var used map[string]string
	depth := 0
	for _, tok := range strings.Fields(condition) {
		switch tok {
		case "{":
			depth++
			continue
		case "}":
			depth--
			continue
		}
		if depth > 0 {
			continue // criteria of an anonymous ACL
		}
		name := strings.TrimPrefix(tok, "!")
		defs, ok := p.acls[name]
		if !ok && defaults != nil {
			defs, ok = defaults.acls[name]
		}
		if !ok {
			continue
		}
		if used == nil {
			used = map[string]string{}
		}
		used[name] = strings.Join(defs, " | ")
	}
	return used
}

func parseRoute(args []string) Route {
	r := Route{Backend: args[0], Dynamic: strings.Contains(args[0], "%[")}
	for i := 1; i < len(args); i++ {
		if args[i] == "if" || args[i] == "unless" {
			r.Condition = strings.Join(args[i:], " ")
			break
		}
	}
	return r
}

// parseBind parses the arguments of a bind line: a comma-separated list of addresses, then options.
func parseBind(args []string) []Bind {
	ssl := false
	for _, a := range args[1:] {
		if a == "ssl" {
			ssl = true
		}
	}
	var binds []Bind
	for _, addr := range strings.Split(args[0], ",") {
		if addr == "" {
			continue
		}
		b := parseAddress(addr)
		b.SSL = ssl
		binds = append(binds, b)
	}
	return binds
}

func parseAddress(raw string) Bind {
	b := Bind{Raw: raw}
	s := raw
	if i := strings.Index(s, "@"); i > 0 && !strings.HasPrefix(s, "/") {
		b.Protocol, s = s[:i], s[i+1:]
	}
	switch {
	case b.Protocol == "unix" || b.Protocol == "abns" || b.Protocol == "fd" || b.Protocol == "sockpair" || strings.HasPrefix(s, "/"):
		if b.Protocol == "" {
			b.Protocol = "unix"
		}
		b.Address = s
		return b
	}

	host, port := s, ""
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host, port = s[:i], s[i+1:]
	}
	host = strings.Trim(host, "[]")
	b.Address, b.Port = host, port
	switch host {
	case "", "*", "0.0.0.0", "::":
		b.Wildcard = true
	}
	if b.Protocol == "" {
		switch {
		case strings.Contains(host, ":"):
			b.Protocol = "ipv6"
		case host != "" && host != "*":
			b.Protocol = "ipv4"
		}
	}
	if host == "*" {
		b.Address = ""
	}
	return b
}

// tokenize splits a configuration line into words, honoring quotes and dropping "#" comments.
func tokenize(line string) []string {
	var toks []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == '#':
			if inWord {
				toks = append(toks, cur.String())
			}
			return toks
		case r == ' ' || r == '\t' || r == '\r':
			if inWord {
				toks = append(toks, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		toks = append(toks, cur.String())
	}
	return toks
}
