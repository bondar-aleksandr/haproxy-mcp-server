package haproxy

import (
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy/hacfg"
	runtimeclient "github.com/tuannvm/haproxy-mcp-server/internal/haproxy/runtime"
)

// BindInfo is an address a frontend listens on.
type BindInfo struct {
	// Address is "ip:port" as configured ("*:443" for all addresses); empty when only the port is known.
	Address  string `json:"address,omitempty"`
	IP       string `json:"ip,omitempty"` // empty = all addresses
	Port     string `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"` // ipv4, ipv6, unix, quic4, ...
	SSL      bool   `json:"ssl,omitempty"`
	Wildcard bool   `json:"wildcard,omitempty"` // listens on every local address
	Source   string `json:"source"`             // "config" (config_file) or "runtime" (listening sockets: port only)

	// Endpoints are the addresses clients connect to: the bind address itself, or for a wildcard
	// bind every address of the instance (its "address" setting, else the hosts of its API URLs) with
	// the port. Empty for sockets, for runtime-only binds (the IP is unknown) and for a wildcard bind
	// when the instance has no known address.
	Endpoints []string `json:"endpoints,omitempty"`
}

// RouteInfo is a way for a frontend to reach a backend.
type RouteInfo struct {
	Backend   string            `json:"backend"`
	Default   bool              `json:"default,omitempty"`   // default_backend: taken when no other rule matches
	Dynamic   bool              `json:"dynamic,omitempty"`   // backend chosen at request time (use_backend %[...])
	Condition string            `json:"condition,omitempty"` // e.g. "if host_app"
	ACLs      map[string]string `json:"acls,omitempty"`      // definitions of the ACLs used in the condition
}

// FrontendInfo is the state of a frontend, as reported by the stats, with what it listens on and
// where it routes to.
type FrontendInfo struct {
	// IID is HAProxy's proxy id (the "iid" column of "show stat", "be_id" in "show servers state"),
	// to correlate with the output of show_stat and the other tools.
	IID    int    `json:"iid,omitempty"`
	Name   string `json:"name"`
	Status string `json:"status"` // OPEN, STOP, ...
	Mode   string `json:"mode,omitempty"`

	Binds    []BindInfo  `json:"binds,omitempty"`
	Routes   []RouteInfo `json:"routes,omitempty"`
	Backends []string    `json:"backends,omitempty"` // every backend reachable from this frontend

	CurrentSessions int   `json:"current_sessions"`
	MaxSessions     int   `json:"max_sessions"`
	SessionLimit    int   `json:"session_limit"`
	TotalSessions   int64 `json:"total_sessions"`
	SessionRate     int   `json:"session_rate"`
	RequestRate     int   `json:"request_rate"`
	TotalRequests   int64 `json:"total_requests"`
	BytesIn         int64 `json:"bytes_in"`
	BytesOut        int64 `json:"bytes_out"`
	DeniedRequests  int64 `json:"denied_requests"`
	RequestErrors   int64 `json:"request_errors"`
	Responses4xx    int64 `json:"http_4xx"`
	Responses5xx    int64 `json:"http_5xx"`

	Stats map[string]string `json:"stats,omitempty"` // the whole stats row (detail "full")
}

// ServerCounts summarizes the servers of a backend by health.
type ServerCounts struct {
	Total int `json:"total"`
	Up    int `json:"up"`
	Down  int `json:"down"`
	Maint int `json:"maint"`
	Other int `json:"other,omitempty"` // draining, no-load-balancing, ...
}

// ServerSummary is a server of a backend.
type ServerSummary struct {
	SID         int    `json:"sid,omitempty"` // server id within its backend ("sid" in "show stat", "srv_id" in "show servers state")
	Name        string `json:"name"`
	Status      string `json:"status"`
	Address     string `json:"address,omitempty"`
	Weight      int    `json:"weight"`
	CheckStatus string `json:"check_status,omitempty"`
	Sessions    int    `json:"current_sessions"`
	State       string `json:"state,omitempty"`       // operational state: RUNNING, STOPPED, ... (Runtime API)
	AdminState  string `json:"admin_state,omitempty"` // srv_admin_state, only when not 0 (Runtime API)

	Stats        map[string]string `json:"stats,omitempty"`         // the whole stats row (detail "full")
	StateDetails map[string]string `json:"state_details,omitempty"` // the whole "show servers state" row (detail "full", Runtime API)
}

// BackendInfo is the state of a backend, with its servers and the frontends that route to it.
type BackendInfo struct {
	IID       int      `json:"iid,omitempty"` // proxy id, see FrontendInfo
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Mode      string   `json:"mode,omitempty"`
	Algorithm string   `json:"algorithm,omitempty"`
	UsedBy    []string `json:"used_by,omitempty"` // frontends routing to it (needs config_file)

	Servers       ServerCounts    `json:"servers"`
	ServerDetails []ServerSummary `json:"server_details"`

	CurrentSessions int   `json:"current_sessions"`
	MaxSessions     int   `json:"max_sessions"`
	SessionLimit    int   `json:"session_limit"`
	QueuedRequests  int   `json:"queued_requests"`
	TotalSessions   int64 `json:"total_sessions"`
	SessionRate     int   `json:"session_rate"`
	BytesIn         int64 `json:"bytes_in"`
	BytesOut        int64 `json:"bytes_out"`
	ConnectErrors   int64 `json:"connection_errors"`
	ResponseErrors  int64 `json:"response_errors"`
	Responses4xx    int64 `json:"http_4xx"`
	Responses5xx    int64 `json:"http_5xx"`

	Stats map[string]string `json:"stats,omitempty"` // the whole stats row (detail "full")
}

// ServiceInfo is what a frontend serves: where it listens and the backends it routes to, with their health.
type ServiceInfo struct {
	IID      int            `json:"iid,omitempty"` // proxy id of the frontend, see FrontendInfo
	Frontend string         `json:"frontend"`
	Status   string         `json:"status"`
	Mode     string         `json:"mode,omitempty"`
	Binds    []BindInfo     `json:"binds,omitempty"`
	Routes   []ServiceRoute `json:"routes,omitempty"`
}

// ServiceRoute is a route of a service, with the state of its backend.
type ServiceRoute struct {
	RouteInfo
	BackendIID    int           `json:"backend_iid,omitempty"`    // proxy id of the backend, if it is running
	BackendStatus string        `json:"backend_status,omitempty"` // empty if the backend is not running
	Servers       *ServerCounts `json:"servers,omitempty"`
}

// Topology is the state of all frontends and backends, joined with the configuration when available.
type Topology struct {
	Frontends []FrontendInfo
	Backends  []BackendInfo
	Warnings  []string
}

// topology builds the state of frontends and backends from the stats, the listening sockets
// (Runtime API) and, when configured, the HAProxy configuration file. The detail level decides how
// much is collected: from DetailStandard on the servers' operational state is read (Runtime API),
// and DetailFull keeps the whole stats and state rows.
func (c *HAProxyClient) topology(level Detail) (*Topology, error) {
	rows, err := c.ShowStat("")
	if err != nil {
		return nil, err
	}
	t := &Topology{}

	cfg, cfgModified, cfgErr := c.loadConfig()
	switch {
	case cfgErr != nil:
		slog.Warn("Cannot read the HAProxy configuration file", "file", c.ConfigFile, "error", cfgErr)
		t.Warnings = append(t.Warnings, fmt.Sprintf("config_file %s cannot be read, bind addresses and routes are limited: %v", c.ConfigFile, cfgErr))
	case cfg == nil:
		t.Warnings = append(t.Warnings, "no config_file configured for this instance: frontend-to-backend routes are not exposed by the Runtime API or the stats page, and bind IPs are not available")
	}
	if cfg != nil {
		if w := c.configDrift(cfgModified); w != "" {
			t.Warnings = append(t.Warnings, w)
		}
	}
	frontendCfg := map[string]hacfg.Frontend{}
	if cfg != nil {
		for _, f := range cfg.Frontends {
			frontendCfg[f.Name] = f
		}
	}
	var listeners map[string][]BindInfo // from "show fd"
	if cfg == nil || cfgErr != nil {
		var warn string
		if listeners, warn = c.runtimeListeners(); warn != "" {
			t.Warnings = append(t.Warnings, warn)
		}
	}

	var states map[string]map[string]string // "show servers state" rows by backend/server
	if level != DetailTerse {
		var warn string
		if states, warn = c.serverStates(); warn != "" {
			t.Warnings = append(t.Warnings, warn)
		}
	}

	servers := map[string][]map[string]string{} // by backend
	for _, row := range rows {
		if row["svname"] != "FRONTEND" && row["svname"] != "BACKEND" && row["type"] == "2" {
			servers[row["pxname"]] = append(servers[row["pxname"]], row)
		}
	}

	usedBy := map[string][]string{}
	for _, row := range rows {
		switch row["svname"] {
		case "FRONTEND":
			f := frontendFromRow(row)
			if level == DetailFull {
				f.Stats = nonEmpty(row)
			}
			if fc, ok := frontendCfg[f.Name]; ok {
				f.Binds, f.Routes = configBinds(fc, c.Addresses), configRoutes(fc)
				if f.Mode == "" {
					f.Mode = fc.Mode
				}
			} else {
				f.Binds = listeners[f.Name]
			}
			f.Backends = routeBackends(f.Routes)
			for _, b := range f.Backends {
				usedBy[b] = append(usedBy[b], f.Name)
			}
			t.Frontends = append(t.Frontends, f)
		case "BACKEND":
			b := backendFromRow(row, servers[row["pxname"]], states, level)
			if level == DetailFull {
				b.Stats = nonEmpty(row)
			}
			t.Backends = append(t.Backends, b)
		}
	}
	for i := range t.Backends {
		t.Backends[i].UsedBy = usedBy[t.Backends[i].Name]
	}
	return t, nil
}

func frontendFromRow(row map[string]string) FrontendInfo {
	return FrontendInfo{
		IID:             int(num(row["iid"])),
		Name:            row["pxname"],
		Status:          row["status"],
		Mode:            row["mode"],
		CurrentSessions: int(num(row["scur"])),
		MaxSessions:     int(num(row["smax"])),
		SessionLimit:    int(num(row["slim"])),
		TotalSessions:   num(row["stot"]),
		SessionRate:     int(num(row["rate"])),
		RequestRate:     int(num(row["req_rate"])),
		TotalRequests:   num(row["req_tot"]),
		BytesIn:         num(row["bin"]),
		BytesOut:        num(row["bout"]),
		DeniedRequests:  num(row["dreq"]),
		RequestErrors:   num(row["ereq"]),
		Responses4xx:    num(row["hrsp_4xx"]),
		Responses5xx:    num(row["hrsp_5xx"]),
	}
}

func backendFromRow(row map[string]string, serverRows []map[string]string, states map[string]map[string]string, level Detail) BackendInfo {
	b := BackendInfo{
		IID:             int(num(row["iid"])),
		Name:            row["pxname"],
		Status:          row["status"],
		Mode:            row["mode"],
		Algorithm:       row["algo"],
		CurrentSessions: int(num(row["scur"])),
		MaxSessions:     int(num(row["smax"])),
		SessionLimit:    int(num(row["slim"])),
		QueuedRequests:  int(num(row["qcur"])),
		TotalSessions:   num(row["stot"]),
		SessionRate:     int(num(row["rate"])),
		BytesIn:         num(row["bin"]),
		BytesOut:        num(row["bout"]),
		ConnectErrors:   num(row["econ"]),
		ResponseErrors:  num(row["eresp"]),
		Responses4xx:    num(row["hrsp_4xx"]),
		Responses5xx:    num(row["hrsp_5xx"]),
		ServerDetails:   []ServerSummary{},
	}
	for _, s := range serverRows {
		srv := ServerSummary{
			SID:         int(num(s["sid"])),
			Name:        s["svname"],
			Status:      s["status"],
			Address:     s["addr"],
			Weight:      int(num(s["weight"])),
			CheckStatus: s["check_status"],
			Sessions:    int(num(s["scur"])),
		}
		if st, ok := states[row["pxname"]+"/"+s["svname"]]; ok {
			srv.State = st["srv_op_state_name"]
			if st["srv_admin_state"] != "0" {
				srv.AdminState = st["srv_admin_state"]
			}
			if level == DetailFull {
				srv.StateDetails = nonEmpty(st)
			}
		}
		if level == DetailFull {
			srv.Stats = nonEmpty(s)
		}
		b.ServerDetails = append(b.ServerDetails, srv)
		b.Servers.Total++
		switch serverClass(s["status"]) {
		case "up":
			b.Servers.Up++
		case "down":
			b.Servers.Down++
		case "maint":
			b.Servers.Maint++
		default:
			b.Servers.Other++
		}
	}
	return b
}

func configBinds(f hacfg.Frontend, instanceAddresses []string) []BindInfo {
	binds := make([]BindInfo, 0, len(f.Binds))
	for _, b := range f.Binds {
		bi := BindInfo{IP: b.Address, Port: b.Port, Protocol: b.Protocol, SSL: b.SSL, Wildcard: b.Wildcard, Source: "config"}
		switch {
		case b.Port == "":
			bi.Address, bi.IP = b.Address, "" // socket path
		case b.Address == "":
			bi.Address = "*:" + b.Port
		case strings.Contains(b.Address, ":"):
			bi.Address = "[" + b.Address + "]:" + b.Port
		default:
			bi.Address = b.Address + ":" + b.Port
		}
		bi.Endpoints = endpoints(b, instanceAddresses)
		binds = append(binds, bi)
	}
	return binds
}

// endpoints returns the addresses clients connect to for a bind, see BindInfo.Endpoints.
func endpoints(b hacfg.Bind, instanceAddresses []string) []string {
	if b.Port == "" {
		return nil // a socket
	}
	if !b.Wildcard {
		return []string{net.JoinHostPort(b.Address, b.Port)}
	}
	var out []string
	for _, a := range instanceAddresses {
		// An IPv4 wildcard (0.0.0.0, ipv4@) cannot be reached on an IPv6 address; names may resolve to either.
		if ip := net.ParseIP(a); ip != nil && ip.To4() == nil && (b.Protocol == "ipv4" || b.Protocol == "quic4") {
			continue
		}
		out = append(out, net.JoinHostPort(a, b.Port))
	}
	return out
}

// configRoutes lists the use_backend rules of a frontend, then its default backend. A "listen"
// section without any routing is its own backend.
func configRoutes(f hacfg.Frontend) []RouteInfo {
	var routes []RouteInfo
	for _, r := range f.Routes {
		routes = append(routes, RouteInfo{Backend: r.Backend, Dynamic: r.Dynamic, Condition: r.Condition, ACLs: r.ACLs})
	}
	switch {
	case f.DefaultBackend != "":
		routes = append(routes, RouteInfo{Backend: f.DefaultBackend, Default: true})
	case f.Listen:
		routes = append(routes, RouteInfo{Backend: f.Name, Default: true})
	}
	return routes
}

// routeBackends returns the distinct static backends of the routes, sorted.
func routeBackends(routes []RouteInfo) []string {
	seen := map[string]bool{}
	var names []string
	for _, r := range routes {
		if !r.Dynamic && !seen[r.Backend] {
			seen[r.Backend] = true
			names = append(names, r.Backend)
		}
	}
	sort.Strings(names)
	return names
}

// --- configuration file ---

type cachedConfig struct {
	mu       sync.Mutex
	modified time.Time // latest modification time of the files that were parsed
	size     int64
	parsed   *hacfg.Config
	path     string
}

// loadConfig returns the parsed configuration file (nil when none is configured) and its
// modification time. The file, or any file of a configuration directory, is re-read when it changes.
func (c *HAProxyClient) loadConfig() (*hacfg.Config, time.Time, error) {
	if c.ConfigFile == "" {
		return nil, time.Time{}, nil
	}
	cache := &c.cfgCache
	cache.mu.Lock()
	defer cache.mu.Unlock()

	modified, size, err := hacfg.Stat(c.ConfigFile)
	if err != nil {
		return nil, time.Time{}, err
	}
	if cache.parsed != nil && cache.path == c.ConfigFile && modified.Equal(cache.modified) && size == cache.size {
		return cache.parsed, modified, nil
	}
	cfg, err := hacfg.Load(c.ConfigFile)
	if err != nil {
		return nil, time.Time{}, err
	}
	cache.parsed, cache.path, cache.modified, cache.size = cfg, c.ConfigFile, modified, size
	return cfg, modified, nil
}

// --- listening sockets (Runtime API) ---

var listenerRe = regexp.MustCompile(`l\.st=\S+ fe=(\S+) fam=(\S+) lport=(\d+)`)

// runtimeListeners returns, per frontend, the ports it listens on according to "show fd". The
// listening IP is not reported by HAProxy there. The second result is a warning when the command
// fails, typically because the Runtime API socket has a lower access level than "show fd" needs.
func (c *HAProxyClient) runtimeListeners() (map[string][]BindInfo, string) {
	if c.RuntimeClient == nil {
		return nil, ""
	}
	out, err := c.RuntimeClient.ExecuteRuntimeCommand("show fd")
	if err != nil {
		slog.Warn("Cannot list the listening sockets", "error", err)
		return nil, fmt.Sprintf("listening ports unavailable: %v", err)
	}
	listeners := map[string][]BindInfo{}
	seen := map[string]bool{}
	for _, m := range listenerRe.FindAllStringSubmatch(out, -1) {
		fe, family, port := m[1], m[2], m[3]
		if fe == "GLOBAL" || seen[fe+":"+port+family] {
			continue
		}
		seen[fe+":"+port+family] = true
		listeners[fe] = append(listeners[fe], BindInfo{Port: port, Protocol: family, Source: "runtime"})
	}
	for _, binds := range listeners {
		sort.Slice(binds, func(i, j int) bool { return num(binds[i].Port) < num(binds[j].Port) })
	}
	return listeners, ""
}

// num parses a stats counter; missing or non-numeric values are 0.
func num(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// serverClass groups the statuses of "show stat": up (UP, "UP 1/3", "no check"), down, maint, other
// (DRAIN, NOLB, ...).
func serverClass(status string) string {
	switch st := strings.ToUpper(status); {
	case strings.HasPrefix(st, "UP"), st == "NO CHECK":
		return "up"
	case strings.HasPrefix(st, "DOWN"):
		return "down"
	case strings.HasPrefix(st, "MAINT"):
		return "maint"
	}
	return "other"
}

// nonEmpty copies a stats/state row without the empty values (HAProxy's "not applicable") and the
// unnamed columns.
func nonEmpty(row map[string]string) map[string]string {
	out := make(map[string]string, len(row))
	for k, v := range row {
		if v != "" && k != "" && k != "-" {
			out[k] = v
		}
	}
	return out
}

// serverStates reads "show servers state" (Runtime API) into rows keyed "backend/server". The
// second result is a warning when that fails; without a Runtime API there is simply no state.
func (c *HAProxyClient) serverStates() (map[string]map[string]string, string) {
	if c.RuntimeClient == nil {
		return nil, ""
	}
	out, err := c.RuntimeClient.ExecuteRuntimeCommand("show servers state")
	if err == nil {
		var rows []map[string]string
		if rows, err = runtimeclient.ParseServersState(out); err == nil {
			states := make(map[string]map[string]string, len(rows))
			for _, r := range rows {
				states[r["be_name"]+"/"+r["srv_name"]] = r
			}
			return states, ""
		}
	}
	slog.Warn("Cannot read the servers state", "error", err)
	return nil, fmt.Sprintf("servers state unavailable: %v", err)
}

// configDrift returns a warning when the configuration file was modified after HAProxy started, i.e.
// when it may no longer be what HAProxy is running. It needs the Runtime API ("show info" tells when
// the process started); without it nothing can be said.
func (c *HAProxyClient) configDrift(modified time.Time) string {
	if c.RuntimeClient == nil {
		return ""
	}
	info, err := c.RuntimeClient.GetProcessInfo()
	if err != nil {
		return ""
	}
	secs, err := strconv.ParseInt(info["Start_time_sec"], 10, 64)
	if err != nil {
		return ""
	}
	return driftWarning(modified, time.Unix(secs, 0))
}

func driftWarning(modified, started time.Time) string {
	if !modified.After(started) {
		return ""
	}
	return fmt.Sprintf("the config file was modified at %s, after HAProxy started at %s: it may not be what is running (HAProxy needs a reload or restart to apply it)",
		modified.UTC().Format(time.RFC3339), started.UTC().Format(time.RFC3339))
}
