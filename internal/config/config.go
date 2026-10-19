package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds the application configuration.
type Config struct {
	// HAProxy instances managed by this server, sorted by name. Never empty.
	Instances []InstanceConfig

	// RetryInterval is how often unavailable instances are retried; 0 disables retrying.
	RetryInterval time.Duration

	// MCP Server Settings
	MCPTransport string // "stdio" or "http"
	MCPPort      int

	// Logging Settings
	LogLevel string
}

// InstanceConfig describes how to reach one HAProxy instance.
type InstanceConfig struct {
	Name        string
	Description string

	// Addresses, when set, are the IPs (or names) HAProxy is reachable at, as reported in the
	// instances resource instead of the hosts of the Runtime API and stats page URLs. Needed when
	// the clients' address differs from the management address, or when there is none (unix socket).
	Addresses []string

	// ConfigFile is the haproxy.cfg of the instance (file, or directory of *.cfg), as readable from
	// this server. Optional: it tells where frontends listen and which backends they route to.
	ConfigFile string

	// Runtime API settings
	RuntimeMode   string // "tcp4" or "unix"; empty disables the Runtime API
	Host          string // tcp4 mode only
	Port          int    // tcp4 mode only
	RuntimeSocket string // unix mode only
	RuntimeURL    string // optional direct URL, overrides the settings above

	// Stats page settings
	StatsEnabled bool
	StatsURL     string // e.g. http://127.0.0.1:8404/stats
}

// RuntimeAPIURL returns the URL of the Runtime API of this instance, or "" when it is not configured.
func (i InstanceConfig) RuntimeAPIURL() (string, error) {
	if i.RuntimeURL != "" {
		return i.RuntimeURL, nil
	}
	switch i.RuntimeMode {
	case "":
		return "", nil
	case "unix":
		if i.RuntimeSocket == "" {
			return "", fmt.Errorf("runtime.socket is empty")
		}
		return (&url.URL{Scheme: "unix", Path: i.RuntimeSocket}).String(), nil
	case "tcp4":
		if i.Host == "" {
			return "", fmt.Errorf("runtime.host is empty")
		}
		return (&url.URL{Scheme: "tcp", Host: fmt.Sprintf("%s:%d", i.Host, i.Port)}).String(), nil
	default:
		return "", fmt.Errorf("invalid runtime mode %q (expected \"tcp4\" or \"unix\")", i.RuntimeMode)
	}
}

// StatsAPIURL returns the URL of the stats page of this instance, or "" when it is disabled.
func (i InstanceConfig) StatsAPIURL() string {
	if i.StatsEnabled {
		return i.StatsURL
	}
	return ""
}

var instanceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// instanceOption is one per-instance key. For the instance "prod" the TOML key is
// "instances.prod.<key>" and the environment variable that overrides it "HAPROXY_PROD_<env>".
type instanceOption struct {
	key string
	env string
	def interface{}
}

var instanceOptions = []instanceOption{
	{"description", "DESCRIPTION", ""},
	{"config_file", "CONFIG_FILE", ""},
	{"address", "ADDRESS", ""},
	{"runtime.mode", "RUNTIME_MODE", ""}, // "tcp4", "unix"; empty = no Runtime API
	{"runtime.host", "HOST", "127.0.0.1"},
	{"runtime.port", "PORT", 9999},
	{"runtime.socket", "RUNTIME_SOCKET", "/var/run/haproxy/admin.sock"},
	{"runtime.url", "RUNTIME_URL", ""},
	{"stats.enabled", "STATS_ENABLED", true},
	{"stats.url", "STATS_URL", ""},
}

// globalOptions are the keys that are not per-instance.
var globalOptions = []struct {
	key, env string
	def      interface{}
}{
	{"haproxy.retry_interval", "HAPROXY_RETRY_INTERVAL", "30s"}, // Go duration, "0" = never retry
	{"mcp.transport", "MCP_TRANSPORT", "http"},                  // "stdio" or "http"
	{"mcp.port", "MCP_PORT", 8080},
	{"log.level", "LOG_LEVEL", "info"},
}

// envName turns an instance name into the form used in environment variable names.
func envName(name string) string {
	return strings.ToUpper(strings.NewReplacer("-", "_").Replace(name))
}

// LoadConfig builds the configuration. Values come, in increasing priority, from the built-in
// defaults, the TOML file at path (skipped when path is empty), and the environment variables.
//
// The HAProxy instances are the tables under [instances.<name>]; at least one is required, so a
// configuration file is mandatory. Environment variables can only override keys of instances (and
// the global settings) that exist: they cannot define new instances.
func LoadConfig(path string) (*Config, error) {
	v := viper.New()
	v.AllowEmptyEnv(true) // an empty env var is a deliberate override (e.g. HAPROXY_PROD_RUNTIME_MODE="")

	if path == "" {
		return nil, fmt.Errorf("no configuration file given (use --config): HAProxy instances are defined in the [instances.<name>] tables")
	}
	v.SetConfigFile(path)
	v.SetConfigType("toml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	known := map[string]bool{}
	for _, o := range globalOptions {
		known[o.key] = true
		v.SetDefault(o.key, o.def)
		if err := v.BindEnv(o.key, o.env); err != nil {
			return nil, err
		}
	}

	// Viper lower-cases keys, so instance names are case-insensitive.
	var names []string
	for name := range v.GetStringMap("instances") {
		names = append(names, name)
	}
	sort.Strings(names)

	cfg := &Config{
		MCPTransport: v.GetString("mcp.transport"),
		MCPPort:      v.GetInt("mcp.port"),
		LogLevel:     v.GetString("log.level"),
	}

	retry, err := time.ParseDuration(v.GetString("haproxy.retry_interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid haproxy.retry_interval (use a duration such as \"30s\", or \"0\" to disable): %w", err)
	}
	cfg.RetryInterval = retry

	if len(names) == 0 {
		return nil, fmt.Errorf("no HAProxy instance defined in %q: add at least one [instances.<name>] table", path)
	}
	for _, name := range names {
		if !instanceNameRe.MatchString(name) {
			return nil, fmt.Errorf("invalid instance name %q: use letters, digits, '-' and '_'", name)
		}
		cfg.Instances = append(cfg.Instances, loadInstance(v, known, "instances."+name, "HAPROXY_"+envName(name)+"_", name))
	}

	for _, k := range v.AllKeys() {
		if !known[k] {
			slog.Warn("Ignoring unknown key in config file", "key", k, "file", path)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadInstance(v *viper.Viper, known map[string]bool, keyPrefix, envPrefix, name string) InstanceConfig {
	for _, o := range instanceOptions {
		key := keyPrefix + "." + o.key
		known[key] = true
		v.SetDefault(key, o.def)
		_ = v.BindEnv(key, envPrefix+o.env)
	}

	return InstanceConfig{
		Name:          name,
		Description:   v.GetString(keyPrefix + ".description"),
		ConfigFile:    v.GetString(keyPrefix + ".config_file"),
		Addresses:     stringList(v.Get(keyPrefix + ".address")),
		RuntimeMode:   v.GetString(keyPrefix + ".runtime.mode"),
		Host:          v.GetString(keyPrefix + ".runtime.host"),
		Port:          v.GetInt(keyPrefix + ".runtime.port"),
		RuntimeSocket: v.GetString(keyPrefix + ".runtime.socket"),
		RuntimeURL:    v.GetString(keyPrefix + ".runtime.url"),
		StatsEnabled:  v.GetBool(keyPrefix + ".stats.enabled"),
		StatsURL:      v.GetString(keyPrefix + ".stats.url"),
	}
}

// validate checks that every instance can be reached through at least one API.
func (c *Config) validate() error {
	if c.RetryInterval < 0 {
		return fmt.Errorf("haproxy.retry_interval must not be negative")
	}
	for _, i := range c.Instances {
		runtimeURL, err := i.RuntimeAPIURL()
		if err != nil {
			return fmt.Errorf("instance %q: %w", i.Name, err)
		}
		if runtimeURL == "" && i.StatsAPIURL() == "" {
			return fmt.Errorf("instance %q: neither the Runtime API nor the stats page is configured", i.Name)
		}
	}
	return nil
}

// LogValue implements slog.LogValuer so that logging a Config does not leak credentials embedded in
// URLs (e.g. http://admin:secret@host:8404/stats).
func (c Config) LogValue() slog.Value {
	instances := make([]any, 0, len(c.Instances))
	for _, i := range c.Instances {
		instances = append(instances, slog.Group(i.Name,
			slog.String("RuntimeMode", i.RuntimeMode),
			slog.String("Host", i.Host),
			slog.Int("Port", i.Port),
			slog.String("RuntimeSocket", i.RuntimeSocket),
			slog.String("RuntimeURL", RedactURL(i.RuntimeURL)),
			slog.Bool("StatsEnabled", i.StatsEnabled),
			slog.String("StatsURL", RedactURL(i.StatsURL)),
		))
	}
	return slog.GroupValue(
		slog.Group("Instances", instances...),
		slog.Duration("RetryInterval", c.RetryInterval),
		slog.String("MCPTransport", c.MCPTransport),
		slog.Int("MCPPort", c.MCPPort),
		slog.String("LogLevel", c.LogLevel),
	)
}

// RedactURL masks the password of a URL with embedded credentials.
func RedactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	return u.Redacted()
}

// stringList reads a setting that is either a TOML array or a string of comma/space separated values
// (the form an environment variable has).
func stringList(v interface{}) []string {
	var raw []string
	switch x := v.(type) {
	case string:
		raw = []string{x}
	case []interface{}:
		for _, e := range x {
			raw = append(raw, fmt.Sprint(e))
		}
	case []string:
		raw = x
	}
	var out []string
	for _, r := range raw {
		for _, f := range strings.FieldsFunc(r, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' }) {
			out = append(out, f)
		}
	}
	return out
}
