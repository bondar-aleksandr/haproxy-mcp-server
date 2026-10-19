package hacfg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const sample = `
global
    log /dev/log local0
    stats socket ipv4@0.0.0.0:9999 level admin

defaults
    mode http
    timeout connect 5s
    default_backend fallback_BE

defaults tcpdefs
    mode tcp

frontend stats
    bind *:8404
    stats enable
    stats uri /stats   # the stats page

frontend prometheus
    bind :8405
    http-request use-service prometheus-exporter if { path /metrics }

frontend web_FE
    bind 10.0.0.5:80,10.0.0.6:80
    bind *:443 ssl crt /etc/ssl/certs/site.pem alpn h2,http/1.1
    bind [2001:db8::1]:443 ssl crt /etc/ssl/certs/site.pem
    bind quic4@0.0.0.0:443 ssl crt /etc/ssl/certs/site.pem
    acl host_folio hdr(host) -i folio.example.com
    acl host_folio hdr(host) -i www.folio.example.com
    acl host_zammad hdr_beg(host) -i zammad.
    acl is_admin path_beg /admin
    use_backend folio_BE if host_folio
    use_backend zammad_BE if host_zammad !is_admin
    use_backend admin_BE if { path_beg /secret } or is_admin
    use_backend %[req.hdr(x-tenant),lower,map(/etc/haproxy/tenants.map)]
    default_backend openproject_BE

frontend db_FE from tcpdefs
    bind unix@/run/haproxy/db.sock
    bind /run/haproxy/db2.sock
    bind abns@db

listen legacy
    bind 192.168.1.10:3306
    server s1 192.168.1.20:3306 check

backend folio_BE
    balance roundrobin
    server folio-ingress 10.10.100.0:80 check

backend zammad_BE
    server zammad 172.18.62.20:3000 check
`

func TestParse(t *testing.T) {
	cfg := Parse(sample)

	// A listen section is a backend too; backends are listed in file order.
	if got, want := cfg.Backends, []string{"legacy", "folio_BE", "zammad_BE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Backends = %v, want %v", got, want)
	}

	names := []string{}
	byName := map[string]Frontend{}
	for _, f := range cfg.Frontends {
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	if want := []string{"stats", "prometheus", "web_FE", "db_FE", "legacy"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("frontends = %v, want %v", names, want)
	}

	stats := byName["stats"]
	if len(stats.Binds) != 1 || !stats.Binds[0].Wildcard || stats.Binds[0].Port != "8404" || stats.Binds[0].Address != "" {
		t.Errorf("stats binds: %+v", stats.Binds)
	}
	if stats.Mode != "http" || stats.DefaultBackend != "fallback_BE" { // from the last defaults before it
		t.Errorf("stats should inherit mode and default_backend from defaults: %+v", stats)
	}
	if p := byName["prometheus"].Binds; len(p) != 1 || p[0].Port != "8405" || !p[0].Wildcard {
		t.Errorf("prometheus binds: %+v", p)
	}

	web := byName["web_FE"]
	wantBinds := []Bind{
		{Raw: "10.0.0.5:80", Protocol: "ipv4", Address: "10.0.0.5", Port: "80"},
		{Raw: "10.0.0.6:80", Protocol: "ipv4", Address: "10.0.0.6", Port: "80"},
		{Raw: "*:443", Port: "443", Wildcard: true, SSL: true},
		{Raw: "[2001:db8::1]:443", Protocol: "ipv6", Address: "2001:db8::1", Port: "443", SSL: true},
		{Raw: "quic4@0.0.0.0:443", Protocol: "quic4", Address: "0.0.0.0", Port: "443", Wildcard: true, SSL: true},
	}
	if !reflect.DeepEqual(web.Binds, wantBinds) {
		t.Errorf("web_FE binds:\n got %+v\nwant %+v", web.Binds, wantBinds)
	}
	if web.DefaultBackend != "openproject_BE" || web.Mode != "http" {
		t.Errorf("web_FE: default %q mode %q", web.DefaultBackend, web.Mode)
	}
	if len(web.Routes) != 4 {
		t.Fatalf("web_FE routes: %+v", web.Routes)
	}
	r := web.Routes
	if r[0].Backend != "folio_BE" || r[0].Condition != "if host_folio" ||
		r[0].ACLs["host_folio"] != "hdr(host) -i folio.example.com | hdr(host) -i www.folio.example.com" {
		t.Errorf("route 0: %+v", r[0])
	}
	if r[1].Condition != "if host_zammad !is_admin" || len(r[1].ACLs) != 2 || r[1].ACLs["is_admin"] != "path_beg /admin" {
		t.Errorf("route 1: %+v", r[1])
	}
	if r[2].Condition != "if { path_beg /secret } or is_admin" || len(r[2].ACLs) != 1 { // inline ACL criteria are not ACL names
		t.Errorf("route 2: %+v", r[2])
	}
	if !r[3].Dynamic || r[3].Condition != "" {
		t.Errorf("route 3 should be dynamic and unconditional: %+v", r[3])
	}

	db := byName["db_FE"]
	if db.Mode != "tcp" || len(db.Binds) != 3 {
		t.Fatalf("db_FE (from tcpdefs): %+v", db)
	}
	if db.DefaultBackend != "" { // tcpdefs has none: it does not inherit from the other defaults section
		t.Errorf("db_FE default_backend = %q", db.DefaultBackend)
	}
	if b := db.Binds[0]; b.Protocol != "unix" || b.Address != "/run/haproxy/db.sock" || b.Port != "" || b.Wildcard {
		t.Errorf("unix bind: %+v", b)
	}
	if b := db.Binds[1]; b.Protocol != "unix" || b.Address != "/run/haproxy/db2.sock" {
		t.Errorf("bare path bind: %+v", b)
	}
	if b := db.Binds[2]; b.Protocol != "abns" || b.Address != "db" {
		t.Errorf("abns bind: %+v", b)
	}

	legacy := byName["legacy"]
	if !legacy.Listen || legacy.Binds[0].Address != "192.168.1.10" || legacy.Binds[0].Port != "3306" {
		t.Errorf("listen section: %+v", legacy)
	}
}

func TestParseBindAddresses(t *testing.T) {
	cases := map[string]Bind{
		":80":               {Raw: ":80", Port: "80", Wildcard: true},
		"*:80":              {Raw: "*:80", Port: "80", Wildcard: true},
		":::443":            {Raw: ":::443", Protocol: "ipv6", Address: "::", Port: "443", Wildcard: true},
		"ipv6@[::]:443":     {Raw: "ipv6@[::]:443", Protocol: "ipv6", Address: "::", Port: "443", Wildcard: true},
		"1.2.3.4:8000-8010": {Raw: "1.2.3.4:8000-8010", Protocol: "ipv4", Address: "1.2.3.4", Port: "8000-8010"},
		"ipv4@10.0.0.1:80":  {Raw: "ipv4@10.0.0.1:80", Protocol: "ipv4", Address: "10.0.0.1", Port: "80"},
	}
	for in, want := range cases {
		if got := parseAddress(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseAddress(%q):\n got %+v\nwant %+v", in, got, want)
		}
	}
}

func TestLoadDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "10-fe.cfg"), []byte("frontend a\n    bind :80\n    default_backend b\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "20-be.cfg"), []byte("backend b\n    server s 1.1.1.1:80\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("frontend ignored\n"), 0o600)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Frontends) != 1 || cfg.Frontends[0].DefaultBackend != "b" || len(cfg.Backends) != 1 {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if _, err := Load(filepath.Join(dir, "missing.cfg")); err == nil {
		t.Error("expected an error for a missing file")
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("expected an error for a directory without *.cfg")
	}
}
