package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// clearEnv removes every variable the config reads, so the host environment does not leak into a test.
func clearEnv(t *testing.T) {
	t.Helper()
	var vars []string
	for _, o := range globalOptions {
		vars = append(vars, o.env)
	}
	for _, o := range instanceOptions {
		vars = append(vars, "HAPROXY_"+o.env, "HAPROXY_PROD_"+o.env, "HAPROXY_DEV_"+o.env, "HAPROXY_EU_WEST_"+o.env)
	}
	for _, name := range vars {
		if old, ok := os.LookupEnv(name); ok {
			t.Cleanup(func() { os.Setenv(name, old) })
			os.Unsetenv(name)
		}
	}
}

func TestInstancesRequired(t *testing.T) {
	clearEnv(t)
	if _, err := LoadConfig(""); err == nil {
		t.Error("expected an error without a config file")
	}
	// No instance, including the former single-instance [haproxy.*] layout.
	for _, content := range []string{"[mcp]\nport = 1\n", "[haproxy.stats]\nurl = \"http://h:8404/stats\"\n"} {
		if _, err := LoadConfig(writeConfig(t, content)); err == nil || !strings.Contains(err.Error(), "no HAProxy instance") {
			t.Errorf("expected a 'no HAProxy instance' error for %q, got %v", content, err)
		}
	}
}

func TestGlobalsFromFileAndEnv(t *testing.T) {
	clearEnv(t)
	content := multiConfig + "\n[log]\nlevel = \"debug\"\n[mcp]\nport = 9090\n"
	t.Setenv("MCP_TRANSPORT", "http")
	cfg, err := LoadConfig(writeConfig(t, strings.Replace(content, "[mcp]\ntransport = \"stdio\"\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "debug" || cfg.MCPPort != 9090 || cfg.MCPTransport != "http" {
		t.Errorf("unexpected globals: %+v", cfg)
	}
}

const multiConfig = `
[mcp]
transport = "stdio"

[instances.prod]
description = "Production edge"
[instances.prod.runtime]
mode = "tcp4"
host = "10.0.0.1"
port = 9999
[instances.prod.stats]
url = "http://admin:secret@10.0.0.1:8404/stats"

[instances.dev]
[instances.dev.stats]
url = "http://10.0.1.1:8404/stats"
`

func TestMultipleInstances(t *testing.T) {
	clearEnv(t)
	cfg, err := LoadConfig(writeConfig(t, multiConfig))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 || cfg.Instances[0].Name != "dev" || cfg.Instances[1].Name != "prod" {
		t.Fatalf("expected sorted [dev prod], got %+v", cfg.Instances)
	}
	dev, prod := cfg.Instances[0], cfg.Instances[1]
	if u, _ := dev.RuntimeAPIURL(); u != "" || dev.StatsAPIURL() != "http://10.0.1.1:8404/stats" {
		t.Errorf("dev should be stats-only: %+v", dev)
	}
	if u, _ := prod.RuntimeAPIURL(); u != "tcp://10.0.0.1:9999" || prod.Description != "Production edge" {
		t.Errorf("unexpected prod: %+v (%q)", prod, u)
	}
}

func TestMultipleInstancesEnvOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("HAPROXY_PROD_STATS_URL", "http://root:pw@10.9.9.9:8404/stats")
	t.Setenv("HAPROXY_PROD_DESCRIPTION", "from env")
	t.Setenv("HAPROXY_DEV_RUNTIME_MODE", "unix")
	t.Setenv("HAPROXY_DEV_RUNTIME_SOCKET", "/run/dev.sock")
	t.Setenv("HAPROXY_STATS_URL", "http://ignored/stats") // variables without an instance name are not read
	cfg, err := LoadConfig(writeConfig(t, multiConfig))
	if err != nil {
		t.Fatal(err)
	}
	dev, prod := cfg.Instances[0], cfg.Instances[1]
	if prod.Description != "from env" {
		t.Errorf("prod description not overridden: %q", prod.Description)
	}
	if prod.StatsURL != "http://root:pw@10.9.9.9:8404/stats" {
		t.Errorf("prod stats url not overridden: %q", prod.StatsURL)
	}
	if u, _ := dev.RuntimeAPIURL(); u != "unix:///run/dev.sock" || dev.StatsURL != "http://10.0.1.1:8404/stats" {
		t.Errorf("unexpected dev: %+v (%q)", dev, u)
	}
}

func TestInstanceValidation(t *testing.T) {
	clearEnv(t)
	cases := map[string]string{
		"no api":         "[instances.a]\n[instances.a.stats]\nenabled = false\n",
		"bad mode":       "[instances.a.runtime]\nmode = \"tcp6\"\n",
		"bad name":       "[instances.\"a b\".stats]\nurl = \"http://x/stats\"\n",
		"unix no socket": "[instances.a.runtime]\nmode = \"unix\"\nsocket = \"\"\n",
	}
	for name, content := range cases {
		if _, err := LoadConfig(writeConfig(t, content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRetryInterval(t *testing.T) {
	clearEnv(t)
	cfg, err := LoadConfig(writeConfig(t, multiConfig))
	if err != nil || cfg.RetryInterval != 30*time.Second {
		t.Fatalf("default retry interval: %v, %v", cfg, err)
	}
	cfg, err = LoadConfig(writeConfig(t, multiConfig+"\n[haproxy]\nretry_interval = \"5s\"\n"))
	if err != nil || cfg.RetryInterval != 5*time.Second {
		t.Fatalf("file retry interval: %v, %v", cfg, err)
	}
	t.Setenv("HAPROXY_RETRY_INTERVAL", "0")
	if cfg, err = LoadConfig(writeConfig(t, multiConfig)); err != nil || cfg.RetryInterval != 0 {
		t.Fatalf("env retry interval: %v, %v", cfg, err)
	}
	for _, bad := range []string{"soon", "-5s"} {
		t.Setenv("HAPROXY_RETRY_INTERVAL", bad)
		if _, err := LoadConfig(writeConfig(t, multiConfig)); err == nil {
			t.Errorf("expected an error for retry interval %q", bad)
		}
	}
}

func TestConfigFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("HAPROXY_EU_WEST_CONFIG_FILE", "/mnt/haproxy/eu-west.cfg")
	content := "[instances.prod]\nconfig_file = \"/etc/haproxy/haproxy.cfg\"\n[instances.prod.stats]\nurl = \"http://h/stats\"\n" +
		"[instances.eu-west.stats]\nurl = \"http://h2/stats\"\n"
	cfg, err := LoadConfig(writeConfig(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Instances[0].ConfigFile; got != "/mnt/haproxy/eu-west.cfg" { // eu-west sorts before prod
		t.Errorf("env override of config_file: %q", got)
	}
	if got := cfg.Instances[1].ConfigFile; got != "/etc/haproxy/haproxy.cfg" {
		t.Errorf("config_file from the file: %q", got)
	}
}

func TestAddress(t *testing.T) {
	clearEnv(t)
	t.Setenv("HAPROXY_DEV_ADDRESS", "10.9.9.1, 10.9.9.2")
	content := "[instances.prod]\naddress = [\"10.0.0.5\", \"10.0.0.6\"]\n[instances.prod.stats]\nurl = \"http://h/stats\"\n" +
		"[instances.dev]\naddress = \"192.168.0.1\"\n[instances.dev.stats]\nurl = \"http://h2/stats\"\n" +
		"[instances.none.stats]\nurl = \"http://h3/stats\"\n"
	cfg, err := LoadConfig(writeConfig(t, content))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, i := range cfg.Instances {
		got[i.Name] = strings.Join(i.Addresses, "|")
	}
	want := map[string]string{"prod": "10.0.0.5|10.0.0.6", "dev": "10.9.9.1|10.9.9.2", "none": ""} // env beats the file
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: addresses %q, want %q", name, got[name], w)
		}
	}
}

func TestEnvNameOfDashedInstance(t *testing.T) {
	clearEnv(t)
	t.Setenv("HAPROXY_EU_WEST_STATS_URL", "http://10.2.2.2:8404/stats")
	cfg, err := LoadConfig(writeConfig(t, "[instances.eu-west.stats]\nurl = \"http://placeholder/stats\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Instances[0].StatsURL; got != "http://10.2.2.2:8404/stats" {
		t.Errorf("stats url = %q", got)
	}
}

func TestBadFiles(t *testing.T) {
	clearEnv(t)
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Error("expected error for missing file")
	}
	if _, err := LoadConfig(writeConfig(t, "this is = not [valid toml")); err == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestLogValueRedactsCredentials(t *testing.T) {
	c := Config{Instances: []InstanceConfig{
		{Name: "a", StatsURL: "http://admin:secret@h:8404/stats", RuntimeURL: "tcp://h:9999"},
	}}
	got := c.LogValue().String()
	if strings.Contains(got, "secret") {
		t.Errorf("password leaked in log value: %s", got)
	}
	if !strings.Contains(got, "tcp://h:9999") {
		t.Errorf("URL without credentials should be kept: %s", got)
	}
}
