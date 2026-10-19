package haproxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const topologyStat = "# pxname,svname,qcur,scur,smax,slim,stot,bin,bout,status,weight,type,mode,algo,addr,check_status,act,bck,hrsp_4xx,hrsp_5xx,iid,sid,\n" +
	"web_FE,FRONTEND,,2,5,1000,12,300,400,OPEN,,0,http,,,,,,1,2,2,0,\n" +
	"admin_FE,FRONTEND,,0,0,1000,0,0,0,STOP,,0,tcp,,,,,,0,0,3,0,\n" +
	"app_BE,app1,0,1,3,,9,0,0,UP,1,2,http,,10.0.0.11:80,L4OK,,,0,0,4,1,\n" +
	"app_BE,app2,0,0,0,,0,0,0,DOWN,1,2,http,,10.0.0.12:80,L4TOUT,,,0,0,4,2,\n" +
	"app_BE,app3,0,0,0,,0,0,0,MAINT,1,2,http,,10.0.0.13:80,,,,0,0,4,3,\n" +
	"app_BE,BACKEND,0,1,3,100,9,10,20,UP,2,1,http,roundrobin,,,2,0,0,1,4,0,\n" +
	"api_BE,api1,0,0,0,,0,0,0,UP 1/3,1,2,http,,10.0.1.1:80,L7OK,,,0,0,5,1,\n" +
	"api_BE,BACKEND,0,0,0,100,0,0,0,UP,1,1,http,leastconn,,,1,0,0,0,5,0,\n" +
	"orphan_BE,BACKEND,0,0,0,100,0,0,0,UP,0,1,http,roundrobin,,,0,0,0,0,6,0,\n"

const topologyCfg = `
defaults
    mode http
frontend web_FE
    bind 10.0.0.5:80
    bind *:443 ssl crt /x.pem
    acl host_api hdr(host) -i api.example.com
    use_backend api_BE if host_api
    default_backend app_BE
frontend admin_FE
    bind 127.0.0.1:9000
backend app_BE
    server app1 10.0.0.11:80
backend api_BE
    server api1 10.0.1.1:80
`

const topologyFD = `      9 : st=0x120121(Cl heopI) owner=0x1 iocb=0x2(sock_accept_iocb) l.st=RDY fe=GLOBAL fam=ipv4 lport=9999
     12 : st=0x120121(Cl heopI) owner=0x1 iocb=0x2(sock_accept_iocb) l.st=RDY fe=web_FE fam=ipv4 lport=80
     13 : st=0x120121(Cl heopI) owner=0x1 iocb=0x2(sock_accept_iocb) l.st=RDY fe=web_FE fam=ipv4 lport=443
     25 : st=0x000121(cl heopI) owner=0x1 iocb=0x3(sock_conn_iocb+0) back=0 cflg=0x80000300 cerr=0 fam=ipv4 lport=443 rport=2638 fe=web_FE mux=H2
     26 : st=0x120121(Cl heopI) owner=0x1 iocb=0x2(sock_accept_iocb) l.st=RDY fe=admin_FE fam=ipv4 lport=9000
`

const topologyServersState = `1
# be_id be_name srv_id srv_name srv_addr srv_op_state srv_admin_state srv_uweight srv_iweight srv_time_since_last_change srv_check_status srv_check_result srv_check_health srv_check_state srv_agent_state bk_f_forced_id srv_f_forced_id srv_fqdn srv_port srvrecord srv_use_ssl srv_check_port srv_check_addr srv_agent_addr srv_agent_port
4 app_BE 1 app1 10.0.0.11 2 0 1 1 100 6 3 4 6 0 0 0 - 80 - 0 0 - - 0
4 app_BE 2 app2 10.0.0.12 0 0 1 1 100 6 3 0 6 0 0 0 - 80 - 0 0 - - 0
4 app_BE 3 app3 10.0.0.13 0 1 1 1 100 6 3 0 6 0 0 0 - 80 - 0 0 - - 0
5 api_BE 1 api1 10.0.1.1 2 0 1 1 100 6 3 4 6 0 0 0 - 80 - 0 0 - - 0
`

// topologyRuntime answers the Runtime API commands the overview tools use.
type topologyRuntime struct {
	RuntimeClient
	started time.Time // when "show info" says the process started; zero: well after any file
}

func (r topologyRuntime) GetProcessInfo() (map[string]string, error) {
	started := r.started
	if started.IsZero() {
		started = time.Now().Add(24 * time.Hour)
	}
	return map[string]string{"Start_time_sec": strconv.FormatInt(started.Unix(), 10)}, nil
}

func (topologyRuntime) ExecuteRuntimeCommand(cmd string) (string, error) {
	switch cmd {
	case "show fd":
		return topologyFD, nil
	case "show servers state":
		return topologyServersState, nil
	}
	return topologyStat, nil
}

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "haproxy.cfg")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTopologyWithConfig(t *testing.T) {
	c := &HAProxyClient{RuntimeClient: topologyRuntime{}, ConfigFile: writeCfg(t, topologyCfg)}

	top, err := c.topology(DetailStandard)
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", top.Warnings)
	}

	if len(top.Frontends) != 2 {
		t.Fatalf("frontends: %+v", top.Frontends)
	}
	web := top.Frontends[0]
	if web.Name != "web_FE" || web.IID != 2 || web.Status != "OPEN" || web.CurrentSessions != 2 {
		t.Errorf("web_FE: %+v", web)
	}
	if len(web.Binds) != 2 || web.Binds[0].Address != "10.0.0.5:80" || web.Binds[0].IP != "10.0.0.5" || web.Binds[0].Port != "80" ||
		web.Binds[1].Address != "*:443" || !web.Binds[1].SSL || !web.Binds[1].Wildcard || web.Binds[1].Source != "config" {
		t.Errorf("web_FE binds: %+v", web.Binds)
	}
	if len(web.Routes) != 2 || web.Routes[0].Backend != "api_BE" || web.Routes[0].Condition != "if host_api" ||
		web.Routes[0].ACLs["host_api"] != "hdr(host) -i api.example.com" || web.Routes[1].Backend != "app_BE" || !web.Routes[1].Default {
		t.Errorf("web_FE routes: %+v", web.Routes)
	}
	if strings.Join(web.Backends, ",") != "api_BE,app_BE" {
		t.Errorf("web_FE backends: %v", web.Backends)
	}
	if admin := top.Frontends[1]; admin.Status != "STOP" || len(admin.Routes) != 0 || admin.Binds[0].Address != "127.0.0.1:9000" {
		t.Errorf("admin_FE: %+v", admin)
	}

	// "standard" reads the operational state of the servers
	if st := top.Backends[0].ServerDetails; len(st) != 3 || st[0].State != "RUNNING" || st[1].State != "STOPPED" || st[2].AdminState != "1" {
		t.Errorf("server states: %+v", st)
	}

	byName := map[string]BackendInfo{}
	for _, b := range top.Backends {
		byName[b.Name] = b
	}
	app := byName["app_BE"]
	if app.IID != 4 || app.ServerDetails[1].SID != 2 || byName["api_BE"].IID != 5 || app.Algorithm != "roundrobin" || app.Status != "UP" || app.Servers != (ServerCounts{Total: 3, Up: 1, Down: 1, Maint: 1}) ||
		len(app.ServerDetails) != 3 || app.ServerDetails[1].Name != "app2" || app.ServerDetails[1].Address != "10.0.0.12:80" ||
		app.ServerDetails[1].CheckStatus != "L4TOUT" || strings.Join(app.UsedBy, ",") != "web_FE" {
		t.Errorf("app_BE: %+v", app)
	}
	if api := byName["api_BE"]; api.Servers.Up != 1 || api.Servers.Total != 1 || api.Algorithm != "leastconn" { // "UP 1/3" counts as up
		t.Errorf("api_BE: %+v", api)
	}
	if orphan := byName["orphan_BE"]; len(orphan.UsedBy) != 0 || orphan.Servers.Total != 0 || orphan.ServerDetails == nil {
		t.Errorf("orphan_BE: %+v", orphan)
	}

	res, warnings, err := c.ListServices("", DetailStandard)
	services, _ := res.([]ServiceInfo)
	if err != nil || len(warnings) != 0 || len(services) != 2 {
		t.Fatalf("ListServices = %+v, %v, %v", res, warnings, err)
	}
	svc := services[0]
	if svc.IID != 2 || svc.Routes[0].BackendIID != 5 || svc.Routes[1].BackendIID != 4 || svc.Frontend != "web_FE" || len(svc.Routes) != 2 || svc.Routes[0].Backend != "api_BE" || svc.Routes[0].BackendStatus != "UP" ||
		svc.Routes[1].Servers == nil || svc.Routes[1].Servers.Down != 1 || svc.Routes[1].Condition != "" || !svc.Routes[1].Default {
		t.Errorf("web_FE service: %+v", svc)
	}
}

func TestTopologyWithoutConfig(t *testing.T) {
	c := &HAProxyClient{RuntimeClient: topologyRuntime{}}
	top, err := c.topology(DetailStandard)
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Warnings) != 1 || !strings.Contains(top.Warnings[0], "config_file") {
		t.Errorf("expected a config_file warning, got %v", top.Warnings)
	}
	web := top.Frontends[0]
	// Without the config only the listening ports are known (from "show fd"), and no routes.
	if len(web.Binds) != 2 || web.Binds[0].Port != "80" || web.Binds[1].Port != "443" || web.Binds[0].Source != "runtime" ||
		web.Binds[0].IP != "" || len(web.Routes) != 0 || len(web.Backends) != 0 {
		t.Errorf("web_FE without config: %+v", web)
	}
	for _, b := range top.Backends {
		if len(b.UsedBy) != 0 {
			t.Errorf("%s: used_by needs the config: %v", b.Name, b.UsedBy)
		}
	}
}

func TestTopologyUnreadableConfig(t *testing.T) {
	c := &HAProxyClient{RuntimeClient: topologyRuntime{}, ConfigFile: "/nonexistent/haproxy.cfg"}
	top, err := c.topology(DetailStandard)
	if err != nil {
		t.Fatal(err) // a broken config_file degrades the result, it does not fail the tool
	}
	if len(top.Warnings) != 1 || !strings.Contains(top.Warnings[0], "/nonexistent/haproxy.cfg") {
		t.Errorf("expected a warning naming the file, got %v", top.Warnings)
	}
	if len(top.Frontends[0].Binds) != 2 { // still the runtime ports
		t.Errorf("runtime binds expected: %+v", top.Frontends[0].Binds)
	}
}

func TestTopologyConfigReloadedOnChange(t *testing.T) {
	path := writeCfg(t, topologyCfg)
	c := &HAProxyClient{RuntimeClient: topologyRuntime{}, ConfigFile: path}
	if top, _ := c.topology(DetailStandard); top.Frontends[0].Routes[1].Backend != "app_BE" {
		t.Fatal("unexpected initial routes")
	}
	changed := strings.Replace(topologyCfg, "default_backend app_BE", "default_backend orphan_BE_changed", 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if top, _ := c.topology(DetailStandard); top.Frontends[0].Routes[1].Backend != "orphan_BE_changed" {
		t.Errorf("the changed config file was not re-read: %+v", top.Frontends[0].Routes)
	}
}

func overviewClient(t *testing.T) *HAProxyClient {
	t.Helper()
	return &HAProxyClient{RuntimeClient: topologyRuntime{}, ConfigFile: writeCfg(t, topologyCfg)}
}

func TestParseDetail(t *testing.T) {
	for in, want := range map[string]Detail{"": DetailTerse, "terse": DetailTerse, "STANDARD": DetailStandard, "full": DetailFull} {
		if got, err := ParseDetail(in); err != nil || got != want {
			t.Errorf("ParseDetail(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseDetail("verbose"); err == nil {
		t.Error("expected an error for an unknown level")
	}
}

func TestListFrontendsLevels(t *testing.T) {
	c := overviewClient(t)

	res, warnings, err := c.ListFrontends("", DetailTerse)
	terse, _ := res.([]FrontendTerse)
	if err != nil || len(warnings) != 0 || len(terse) != 2 {
		t.Fatalf("terse: %+v, %v, %v", res, warnings, err)
	}
	if web := terse[0]; web.IID != 2 || web.Name != "web_FE" || web.Status != "OPEN" ||
		strings.Join(web.Binds, "|") != "10.0.0.5:80|*:443 ssl" || strings.Join(web.Backends, ",") != "api_BE,app_BE" {
		t.Errorf("terse web_FE: %+v", web)
	}
	if terse[1].Status != "STOP" || len(terse[1].Backends) != 0 {
		t.Errorf("terse admin_FE: %+v", terse[1])
	}

	res, _, _ = c.ListFrontends("web_FE", DetailStandard)
	std, _ := res.([]FrontendInfo)
	if len(std) != 1 || std[0].CurrentSessions != 2 || len(std[0].Routes) != 2 || std[0].Routes[0].ACLs["host_api"] == "" || std[0].Stats != nil {
		t.Errorf("standard: %+v", std)
	}

	res, _, _ = c.ListFrontends("web_FE", DetailFull)
	full, _ := res.([]FrontendInfo)
	if len(full) != 1 || full[0].Stats["pxname"] != "web_FE" || full[0].Stats["status"] != "OPEN" || full[0].Stats["slim"] != "1000" {
		t.Errorf("full stats row: %+v", full)
	}
	if _, present := full[0].Stats["algo"]; present { // empty columns are left out
		t.Errorf("empty columns should be dropped: %v", full[0].Stats)
	}

	if _, _, err := c.ListFrontends("nope", DetailTerse); err == nil || !strings.Contains(err.Error(), "admin_FE, web_FE") {
		t.Errorf("unknown frontend should list the known ones, got %v", err)
	}
}

func TestListBackendsLevels(t *testing.T) {
	c := overviewClient(t)

	res, _, err := c.ListBackends("", "", DetailTerse)
	terse, _ := res.([]BackendTerse)
	if err != nil || len(terse) != 3 {
		t.Fatalf("terse: %+v, %v", res, err)
	}
	app := terse[0]
	if app.IID != 4 || app.Name != "app_BE" || app.Servers != (ServerCounts{Total: 3, Up: 1, Down: 1, Maint: 1}) ||
		strings.Join(app.UsedBy, ",") != "web_FE" || len(app.Problems) != 2 {
		t.Errorf("terse app_BE: %+v", app)
	}
	if p := app.Problems[0]; p.Name != "app2" || p.SID != 2 || p.Status != "DOWN" || p.CheckStatus != "L4TOUT" { // app1 is UP: not a problem
		t.Errorf("problems: %+v", app.Problems)
	}
	if api := terse[1]; len(api.Problems) != 0 { // "UP 1/3" is up
		t.Errorf("terse api_BE: %+v", api)
	}

	res, _, _ = c.ListBackends("app_BE", "", DetailStandard)
	std, _ := res.([]BackendInfo)
	if len(std) != 1 || len(std[0].ServerDetails) != 3 || std[0].ServerDetails[0].State != "RUNNING" || std[0].CurrentSessions != 1 ||
		std[0].Stats != nil || std[0].ServerDetails[0].Stats != nil || std[0].ServerDetails[0].StateDetails != nil {
		t.Errorf("standard: %+v", std)
	}

	// "server" narrows to one server and implies at least standard
	res, _, err = c.ListBackends("app_BE", "app2", DetailTerse)
	one, _ := res.([]BackendInfo)
	if err != nil || len(one) != 1 || len(one[0].ServerDetails) != 1 || one[0].ServerDetails[0].Name != "app2" || one[0].Servers.Total != 3 {
		t.Fatalf("server filter: %+v, %v", res, err)
	}

	res, _, _ = c.ListBackends("app_BE", "app2", DetailFull)
	full, _ := res.([]BackendInfo)
	srv := full[0].ServerDetails[0]
	if full[0].Stats["algo"] != "roundrobin" || srv.Stats["svname"] != "app2" || srv.Stats["check_status"] != "L4TOUT" ||
		srv.StateDetails["srv_op_state_name"] != "STOPPED" || srv.StateDetails["srv_uweight"] != "1" {
		t.Errorf("full: backend stats %v, server %+v", full[0].Stats, srv)
	}

	if _, _, err := c.ListBackends("nope", "", DetailTerse); err == nil || !strings.Contains(err.Error(), "api_BE, app_BE, orphan_BE") {
		t.Errorf("unknown backend: %v", err)
	}
	if _, _, err := c.ListBackends("app_BE", "ghost", DetailTerse); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("unknown server: %v", err)
	}
	if _, _, err := c.ListBackends("", "app1", DetailTerse); err == nil {
		t.Error("server without name should fail")
	}
}

func TestListServicesTerse(t *testing.T) {
	c := overviewClient(t)
	res, _, err := c.ListServices("web_FE", DetailTerse)
	terse, _ := res.([]ServiceTerse)
	if err != nil || len(terse) != 1 {
		t.Fatalf("%+v, %v", res, err)
	}
	svc := terse[0]
	if svc.IID != 2 || strings.Join(svc.Binds, "|") != "10.0.0.5:80|*:443 ssl" || len(svc.Routes) != 2 {
		t.Fatalf("service: %+v", svc)
	}
	// The terse view says which hosts go where, not just the names of the ACLs.
	if r := svc.Routes[0]; r.Backend != "api_BE" || r.Condition != "if host_api" || r.ACLs["host_api"] != "hdr(host) -i api.example.com" {
		t.Errorf("first route: %+v", r)
	}
	if r := svc.Routes[1]; r.Backend != "app_BE" || r.BackendIID != 4 || !r.Default || r.Status != "UP" || r.ServersUp != 1 || r.ServersTotal != 3 {
		t.Errorf("default route: %+v", r)
	}
	if _, _, err := c.ListServices("nope", DetailTerse); err == nil {
		t.Error("unknown frontend should fail")
	}
}

// Without a Runtime API (stats page only) the servers' operational state is simply absent.
func TestOverviewFromStatsPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(backendsJSON))
	}))
	defer srv.Close()
	c, err := NewHAProxyClient("", srv.URL+"/stats")
	if err != nil {
		t.Fatal(err)
	}

	res, warnings, err := c.ListBackends("be", "srv1", DetailFull)
	full, _ := res.([]BackendInfo)
	if err != nil || len(full) != 1 || len(full[0].ServerDetails) != 1 {
		t.Fatalf("%+v, %v", res, err)
	}
	s := full[0].ServerDetails[0]
	if s.Name != "srv1" || s.Address != "10.0.0.1:80" || s.Status != "DOWN" || s.State != "" || s.StateDetails != nil || s.Stats["addr"] != "10.0.0.1:80" {
		t.Errorf("server: %+v", s)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "config_file") { // and no warning about the servers state
		t.Errorf("warnings: %v", warnings)
	}

	res, _, _ = c.ListFrontends("", DetailTerse)
	if fes, _ := res.([]FrontendTerse); len(fes) != 1 || fes[0].Name != "fe" {
		t.Errorf("frontends: %+v", res)
	}
}

func TestBindEndpoints(t *testing.T) {
	cfg := `
frontend web_FE
    bind 10.0.0.5:80
    bind *:443 ssl crt /x.pem
    bind 0.0.0.0:8080
    bind [::]:9000
    bind unix@/run/web.sock
frontend admin_FE
    bind :8404
`
	c := &HAProxyClient{
		RuntimeClient: topologyRuntime{}, ConfigFile: writeCfg(t, cfg),
		Addresses: []string{"172.18.62.7", "2001:db8::1"},
	}

	res, _, err := c.ListFrontends("web_FE", DetailTerse)
	terse, _ := res.([]FrontendTerse)
	if err != nil || len(terse) != 1 {
		t.Fatalf("%+v, %v", res, err)
	}
	want := []string{
		"10.0.0.5:80", // not a wildcard: the bind address is where clients connect, nothing to add
		"*:443 ssl -> 172.18.62.7:443, [2001:db8::1]:443",
		"0.0.0.0:8080 -> 172.18.62.7:8080", // an IPv4 wildcard is not reachable on the IPv6 address
		"[::]:9000 -> 172.18.62.7:9000, [2001:db8::1]:9000",
		"/run/web.sock",
	}
	if got := terse[0].Binds; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("terse binds:\n got %q\nwant %q", got, want)
	}

	res, _, _ = c.ListFrontends("web_FE", DetailStandard)
	std, _ := res.([]FrontendInfo)
	binds := std[0].Binds
	if strings.Join(binds[0].Endpoints, ",") != "10.0.0.5:80" || binds[0].Wildcard {
		t.Errorf("specific bind: %+v", binds[0])
	}
	if strings.Join(binds[1].Endpoints, ",") != "172.18.62.7:443,[2001:db8::1]:443" || !binds[1].Wildcard || !binds[1].SSL {
		t.Errorf("wildcard bind: %+v", binds[1])
	}
	if len(binds[4].Endpoints) != 0 {
		t.Errorf("a socket has no endpoint: %+v", binds[4])
	}

	// The same through list_services.
	res, _, _ = c.ListServices("web_FE", DetailTerse)
	if svc, _ := res.([]ServiceTerse); len(svc) != 1 || svc[0].Binds[1] != want[1] {
		t.Errorf("service binds: %+v", res)
	}

	// Without a known address a wildcard bind is shown as written; with a hostname it is joined as is.
	c.Addresses = nil
	res, _, _ = c.ListFrontends("admin_FE", DetailTerse)
	if fe, _ := res.([]FrontendTerse); len(fe) != 1 || strings.Join(fe[0].Binds, ",") != "*:8404" {
		t.Errorf("no address: %+v", res)
	}
	c.Addresses = []string{"lb.example.com"}
	res, _, _ = c.ListFrontends("admin_FE", DetailTerse)
	if fe, _ := res.([]FrontendTerse); len(fe) != 1 || strings.Join(fe[0].Binds, ",") != "*:8404 -> lb.example.com:8404" {
		t.Errorf("hostname: %+v", res)
	}

	// Binds known from the runtime only (no config_file) have no IP, hence no endpoints.
	c = &HAProxyClient{RuntimeClient: topologyRuntime{}, Addresses: []string{"172.18.62.7"}}
	res, _, _ = c.ListFrontends("web_FE", DetailTerse)
	if fe, _ := res.([]FrontendTerse); len(fe) != 1 || strings.Join(fe[0].Binds, ",") != "port 80,port 443" {
		t.Errorf("runtime-only binds: %+v", res)
	}
}

// fdDenied is a Runtime API whose socket does not allow "show fd" (access level "user").
type fdDenied struct{ topologyRuntime }

func (f fdDenied) ExecuteRuntimeCommand(cmd string) (string, error) {
	if cmd == "show fd" {
		return "", fmt.Errorf("HAProxy refused %q: Permission denied", cmd)
	}
	return f.topologyRuntime.ExecuteRuntimeCommand(cmd)
}

func TestListeningPortsWarning(t *testing.T) {
	c := &HAProxyClient{RuntimeClient: fdDenied{}}
	res, warnings, err := c.ListFrontends("web_FE", DetailTerse)
	fe, _ := res.([]FrontendTerse)
	if err != nil || len(fe) != 1 || len(fe[0].Binds) != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
	found := false
	for _, w := range warnings {
		found = found || strings.Contains(w, "listening ports unavailable") && strings.Contains(w, "Permission denied")
	}
	if !found {
		t.Errorf("a failing show fd must be reported, got %v", warnings)
	}
}

type debugRuntime struct{ RuntimeClient }

func (debugRuntime) ExecuteRuntimeCommand(cmd string) (string, error) {
	if cmd != "debug counters" {
		return "", fmt.Errorf("unexpected command %q", cmd)
	}
	return "Count     Type Location function(): \"condition\" [comment]\n" +
		"3          GLT h2.c:5124 h2_detect_glitch(): \"too many glitches\"\n" +
		"12         CNT fd.c:99 fd_update(): \"x > y\" [note]\n", nil
}

func TestDebugCounters(t *testing.T) {
	c := &HAProxyClient{RuntimeClient: debugRuntime{}}
	got, err := c.DebugCounters()
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	if got[0].Count != 3 || got[0].Type != "GLT" || !strings.HasPrefix(got[0].Location, "h2.c:5124 h2_detect_glitch()") ||
		got[1].Count != 12 || !strings.Contains(got[1].Location, "[note]") {
		t.Errorf("unexpected counters: %+v", got)
	}
}

func TestConfigDriftWarning(t *testing.T) {
	path := writeCfg(t, topologyCfg)
	modified := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}

	// HAProxy started after the file was last modified: nothing to say.
	c := &HAProxyClient{RuntimeClient: topologyRuntime{started: modified.Add(time.Minute)}, ConfigFile: path}
	if top, err := c.topology(DetailTerse); err != nil || len(top.Warnings) != 0 {
		t.Fatalf("%v, %v", top, err)
	}

	// The file was edited after HAProxy started: the overview says it may not be what is running.
	c = &HAProxyClient{RuntimeClient: topologyRuntime{started: modified.Add(-time.Minute)}, ConfigFile: path}
	top, err := c.topology(DetailTerse)
	if err != nil || len(top.Warnings) != 1 || !strings.Contains(top.Warnings[0], "after HAProxy started") {
		t.Fatalf("expected a drift warning, got %v, %v", top.Warnings, err)
	}

	// Without a Runtime API the start time is unknown: no claim either way.
	c = &HAProxyClient{StatsClient: nil, ConfigFile: path}
	if w := c.configDrift(modified); w != "" {
		t.Errorf("no runtime, no warning: %q", w)
	}
}

func instanceWithConfig(t *testing.T, text string) *Instance {
	t.Helper()
	return &Instance{Name: "prod", ConfigFile: writeCfg(t, text)}
}

func TestShowConfig(t *testing.T) {
	text := `global
    setenv ACCOUNT_THUMBPRINT 'abc123'

# sites
frontend web_FE
    bind :80
    stats auth admin:hunter2

backend app_BE
    server a 10.0.0.1:80 check
`
	inst := instanceWithConfig(t, text)

	d, err := inst.ShowConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Text, "abc123") || strings.Contains(d.Text, "hunter2") || d.Redacted != 2 || d.Lines != 10 || d.Sections != nil {
		t.Errorf("whole config: redacted=%d lines=%d\n%s", d.Redacted, d.Lines, d.Text)
	}
	if !strings.Contains(d.Text, "# sites\nfrontend web_FE\n    bind :80\n") || !strings.Contains(d.Text, "setenv ACCOUNT_THUMBPRINT <redacted>") {
		t.Errorf("the rest must stay as written:\n%s", d.Text)
	}

	d, err = inst.ShowConfig("web_FE")
	if err != nil || len(d.Sections) != 1 || d.Sections[0].Header != "frontend web_FE" || d.Sections[0].FirstLine != 4 ||
		d.Redacted != 1 || strings.Contains(d.Text, "global") || strings.Contains(d.Text, "app_BE") {
		t.Errorf("filtered: %+v\n%s (%v)", d, d.Text, err)
	}

	if _, err := inst.ShowConfig("nope"); err == nil || !strings.Contains(err.Error(), "global, frontend web_FE, backend app_BE") {
		t.Errorf("no match should list the sections, got %v", err)
	}
	if _, err := (&Instance{Name: "x"}).ShowConfig(""); err == nil || !strings.Contains(err.Error(), "no config_file") {
		t.Errorf("no config_file: %v", err)
	}
	if _, err := (&Instance{Name: "x", ConfigFile: "/nonexistent/haproxy.cfg"}).ShowConfig(""); err == nil {
		t.Error("an unreadable file must fail")
	}

	// The drift check uses the instance's client when it is connected.
	path := inst.ConfigFile
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(path, old, old)
	inst.SetClient(&HAProxyClient{RuntimeClient: topologyRuntime{started: old.Add(-time.Hour)}})
	if d, _ := inst.ShowConfig(""); len(d.Warnings) != 1 {
		t.Errorf("drift warning expected: %v", d.Warnings)
	}
}
