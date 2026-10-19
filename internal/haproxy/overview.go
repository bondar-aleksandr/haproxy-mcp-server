package haproxy

import (
	"fmt"
	"sort"
	"strings"
)

// Detail is how much the overview tools report about each object.
type Detail string

const (
	// DetailTerse is the default first look: identity, status, health summary and what is wrong.
	DetailTerse Detail = "terse"
	// DetailStandard adds counters, the table of servers and the routes with their conditions.
	DetailStandard Detail = "standard"
	// DetailFull adds every column HAProxy reports for the object (stats row, servers state row).
	DetailFull Detail = "full"
)

// ParseDetail parses a detail level; an empty string is DetailTerse.
func ParseDetail(s string) (Detail, error) {
	switch d := Detail(strings.ToLower(s)); d {
	case "", DetailTerse:
		return DetailTerse, nil
	case DetailStandard, DetailFull:
		return d, nil
	}
	return "", fmt.Errorf("invalid detail %q: use terse, standard or full", s)
}

// FrontendTerse is the first look at a frontend.
type FrontendTerse struct {
	IID      int      `json:"iid,omitempty"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Mode     string   `json:"mode,omitempty"`
	Binds    []string `json:"binds,omitempty"`    // "10.0.0.5:80", "*:443 ssl -> 172.18.62.7:443", "port 80" when the IP is unknown
	Backends []string `json:"backends,omitempty"` // backends it routes to (needs config_file)
}

// ServerProblem is a server that is not UP.
type ServerProblem struct {
	SID         int    `json:"sid,omitempty"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	CheckStatus string `json:"check_status,omitempty"`
}

// BackendTerse is the first look at a backend.
type BackendTerse struct {
	IID      int             `json:"iid,omitempty"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Servers  ServerCounts    `json:"servers"`
	UsedBy   []string        `json:"used_by,omitempty"`  // frontends routing to it (needs config_file)
	Problems []ServerProblem `json:"problems,omitempty"` // servers that are not UP
}

// ServiceTerse is the first look at a service: a frontend, where it listens and what it routes to.
type ServiceTerse struct {
	IID      int                 `json:"iid,omitempty"`
	Frontend string              `json:"frontend"`
	Status   string              `json:"status"`
	Binds    []string            `json:"binds,omitempty"`
	Routes   []ServiceRouteTerse `json:"routes,omitempty"`
}

// ServiceRouteTerse is a route of a service with a one-line state of its backend.
type ServiceRouteTerse struct {
	Backend    string `json:"backend"`
	BackendIID int    `json:"backend_iid,omitempty"`
	Condition  string `json:"condition,omitempty"` // "if ACL_app", "unless is_admin"
	// ACLs defines the named ACLs used in the condition (e.g. "ACL_app": "hdr(host) -i app.example.com"):
	// which hosts or paths go to the backend.
	ACLs         map[string]string `json:"acls,omitempty"`
	Default      bool              `json:"default,omitempty"`
	Dynamic      bool              `json:"dynamic,omitempty"`
	Status       string            `json:"status,omitempty"` // backend status; empty if the backend is not running
	ServersUp    int               `json:"servers_up"`
	ServersTotal int               `json:"servers_total"`
}

// short renders a bind the way it is written in the configuration, with where clients connect for
// a wildcard: "*:443 ssl -> 172.18.62.7:443".
func (b BindInfo) short() string {
	var s string
	switch {
	case b.Address != "":
		s = b.Address
	case b.Port != "":
		s = "port " + b.Port // the IP is not known (no config_file)
	}
	if b.SSL {
		s += " ssl"
	}
	if b.Wildcard && len(b.Endpoints) > 0 {
		s += " -> " + strings.Join(b.Endpoints, ", ")
	}
	return s
}

func shortBinds(binds []BindInfo) []string {
	var out []string
	for _, b := range binds {
		out = append(out, b.short())
	}
	return out
}

// ListFrontends returns the frontends: FrontendTerse items for DetailTerse, else FrontendInfo.
// name restricts the result to one frontend.
func (c *HAProxyClient) ListFrontends(name string, d Detail) (interface{}, []string, error) {
	t, err := c.topology(d)
	if err != nil {
		return nil, nil, err
	}
	frontends := t.Frontends
	if name != "" {
		frontends = nil
		for _, f := range t.Frontends {
			if f.Name == name {
				frontends = append(frontends, f)
			}
		}
		if frontends == nil {
			return nil, nil, notFound("frontend", name, frontendNames(t.Frontends))
		}
	}
	if d != DetailTerse {
		return frontends, t.Warnings, nil
	}
	terse := make([]FrontendTerse, 0, len(frontends))
	for _, f := range frontends {
		terse = append(terse, FrontendTerse{IID: f.IID, Name: f.Name, Status: f.Status, Mode: f.Mode, Binds: shortBinds(f.Binds), Backends: f.Backends})
	}
	return terse, t.Warnings, nil
}

// ListBackends returns the backends: BackendTerse items for DetailTerse, else BackendInfo. name
// restricts the result to one backend and server, which needs name, to one of its servers (the
// table of servers is then reported, so it implies at least DetailStandard).
func (c *HAProxyClient) ListBackends(name, server string, d Detail) (interface{}, []string, error) {
	if server != "" {
		if name == "" {
			return nil, nil, fmt.Errorf("\"server\" needs \"name\", the backend containing the server")
		}
		if d == DetailTerse {
			d = DetailStandard
		}
	}
	t, err := c.topology(d)
	if err != nil {
		return nil, nil, err
	}
	backends := t.Backends
	if name != "" {
		backends = nil
		for _, b := range t.Backends {
			if b.Name == name {
				backends = append(backends, b)
			}
		}
		if backends == nil {
			return nil, nil, notFound("backend", name, backendNames(t.Backends))
		}
	}
	if server != "" {
		b := backends[0]
		var kept []ServerSummary
		for _, s := range b.ServerDetails {
			if s.Name == server {
				kept = append(kept, s)
			}
		}
		if kept == nil {
			return nil, nil, fmt.Errorf("server %q not found in backend %q", server, name)
		}
		b.ServerDetails = kept
		backends = []BackendInfo{b}
	}
	if d != DetailTerse {
		return backends, t.Warnings, nil
	}
	terse := make([]BackendTerse, 0, len(backends))
	for _, b := range backends {
		bt := BackendTerse{IID: b.IID, Name: b.Name, Status: b.Status, Servers: b.Servers, UsedBy: b.UsedBy}
		for _, s := range b.ServerDetails {
			if serverClass(s.Status) != "up" {
				bt.Problems = append(bt.Problems, ServerProblem{SID: s.SID, Name: s.Name, Status: s.Status, CheckStatus: s.CheckStatus})
			}
		}
		terse = append(terse, bt)
	}
	return terse, t.Warnings, nil
}

// ListServices returns the services, one per frontend, with the health of the backends they route
// to: ServiceTerse items for DetailTerse, else ServiceInfo (binds in full, ACL definitions, server
// counts). name restricts the result to one frontend. DetailFull is the same as DetailStandard.
func (c *HAProxyClient) ListServices(name string, d Detail) (interface{}, []string, error) {
	t, err := c.topology(DetailTerse)
	if err != nil {
		return nil, nil, err
	}
	backends := map[string]BackendInfo{}
	for _, b := range t.Backends {
		backends[b.Name] = b
	}
	var terse []ServiceTerse
	var standard []ServiceInfo
	for _, f := range t.Frontends {
		if name != "" && f.Name != name {
			continue
		}
		st := ServiceTerse{IID: f.IID, Frontend: f.Name, Status: f.Status, Binds: shortBinds(f.Binds)}
		si := ServiceInfo{IID: f.IID, Frontend: f.Name, Status: f.Status, Mode: f.Mode, Binds: f.Binds}
		for _, r := range f.Routes {
			rt := ServiceRouteTerse{Backend: r.Backend, Condition: r.Condition, ACLs: r.ACLs, Default: r.Default, Dynamic: r.Dynamic}
			sr := ServiceRoute{RouteInfo: r}
			if b, ok := backends[r.Backend]; ok {
				counts := b.Servers
				rt.BackendIID, rt.Status, rt.ServersUp, rt.ServersTotal = b.IID, b.Status, b.Servers.Up, b.Servers.Total
				sr.BackendIID, sr.BackendStatus, sr.Servers = b.IID, b.Status, &counts
			}
			st.Routes = append(st.Routes, rt)
			si.Routes = append(si.Routes, sr)
		}
		terse = append(terse, st)
		standard = append(standard, si)
	}
	if name != "" && len(terse) == 0 {
		return nil, nil, notFound("frontend", name, frontendNames(t.Frontends))
	}
	if d == DetailTerse {
		if terse == nil {
			terse = []ServiceTerse{}
		}
		return terse, t.Warnings, nil
	}
	if standard == nil {
		standard = []ServiceInfo{}
	}
	return standard, t.Warnings, nil
}

func frontendNames(fs []FrontendInfo) []string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Name
	}
	return names
}

func backendNames(bs []BackendInfo) []string {
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	return names
}

func notFound(kind, name string, known []string) error {
	sort.Strings(known)
	return fmt.Errorf("%s %q not found, one of: %s", kind, name, strings.Join(known, ", "))
}
