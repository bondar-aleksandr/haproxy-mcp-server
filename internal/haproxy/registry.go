package haproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Instance is a named HAProxy instance managed by the server. It is declared by the configuration and
// becomes available once a connection to it has succeeded; until then (and again if a later connection
// attempt fails) it is reported as unavailable.
type Instance struct {
	Name        string
	Description string

	// RuntimeURL and StatsURL are the configured endpoints ("" when the API is not configured).
	// They may contain credentials: never expose them as they are.
	RuntimeURL string
	StatsURL   string

	// ConfigFile is the instance's haproxy.cfg, if one is configured.
	ConfigFile string

	// Addresses overrides the addresses derived from RuntimeURL and StatsURL, see InstanceInfo.
	Addresses []string

	// Connect establishes the connection to the instance. It is called at startup and then
	// periodically until it succeeds.
	Connect func() (*HAProxyClient, error)

	mu          sync.RWMutex
	client      *HAProxyClient
	lastErr     string
	lastAttempt time.Time
	attempts    int
}

// HasRuntime reports whether the Runtime API is configured for the instance.
func (i *Instance) HasRuntime() bool { return i.RuntimeURL != "" }

// HasStats reports whether the stats page is configured for the instance.
func (i *Instance) HasStats() bool { return i.StatsURL != "" }

// Client returns the client of the instance, or an error describing why it is unavailable.
func (i *Instance) Client() (*HAProxyClient, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.client == nil {
		if i.lastErr == "" {
			return nil, fmt.Errorf("instance %q is unavailable: not connected yet", i.Name)
		}
		return nil, fmt.Errorf("instance %q is unavailable: %s", i.Name, i.lastErr)
	}
	return i.client, nil
}

// LastError returns the (credential-free) reason the last connection attempt failed, or "".
func (i *Instance) LastError() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.lastErr
}

// Available reports whether the instance is connected.
func (i *Instance) Available() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.client != nil
}

// SetClient marks the instance as available with the given client.
func (i *Instance) SetClient(c *HAProxyClient) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.client, i.lastErr = c, ""
}

// TryConnect runs Connect and records the outcome.
func (i *Instance) TryConnect() error {
	if i.Connect == nil {
		return fmt.Errorf("instance %q has no way to connect", i.Name)
	}
	client, err := i.Connect()

	i.mu.Lock()
	defer i.mu.Unlock()
	i.lastAttempt = time.Now()
	i.attempts++
	if err != nil {
		i.client, i.lastErr = nil, i.scrub(err.Error())
		return err
	}
	i.client, i.lastErr = client, ""
	return nil
}

// scrub masks the credentials of the configured URLs in a message.
func (i *Instance) scrub(msg string) string {
	for _, raw := range []string{i.RuntimeURL, i.StatsURL} {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			continue
		}
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "xxxxx")
		}
	}
	return msg
}

// Registry is the set of HAProxy instances declared in the configuration.
type Registry struct {
	instances []*Instance // sorted by name
	byName    map[string]*Instance
}

// NewRegistry builds a registry from the given instances. Names must be unique and there must be at
// least one instance.
func NewRegistry(instances ...*Instance) (*Registry, error) {
	if len(instances) == 0 {
		return nil, fmt.Errorf("at least one HAProxy instance is required")
	}
	r := &Registry{byName: make(map[string]*Instance, len(instances))}
	for _, i := range instances {
		if _, dup := r.byName[i.Name]; dup {
			return nil, fmt.Errorf("duplicate HAProxy instance name %q", i.Name)
		}
		r.byName[i.Name] = i
		r.instances = append(r.instances, i)
	}
	sort.Slice(r.instances, func(a, b int) bool { return r.instances[a].Name < r.instances[b].Name })
	return r, nil
}

// Instances returns all instances, sorted by name.
func (r *Registry) Instances() []*Instance { return r.instances }

// Names returns the names of all configured instances, sorted, whether they are available or not.
func (r *Registry) Names() []string {
	names := make([]string, len(r.instances))
	for i, inst := range r.instances {
		names[i] = inst.Name
	}
	return names
}

// Resolve returns the instance with the given name. An empty name selects the only instance when
// there is exactly one, and is an error otherwise. The instance may be unavailable.
func (r *Registry) Resolve(name string) (*Instance, error) {
	if name == "" {
		if len(r.instances) == 1 {
			return r.instances[0], nil
		}
		return nil, fmt.Errorf("parameter \"instance\" is required, one of: %s", strings.Join(r.Names(), ", "))
	}
	if inst, ok := r.byName[name]; ok {
		return inst, nil
	}
	return nil, fmt.Errorf("unknown HAProxy instance %q, one of: %s", name, strings.Join(r.Names(), ", "))
}

// AnyRuntime reports whether the Runtime API is configured for at least one instance.
func (r *Registry) AnyRuntime() bool {
	for _, i := range r.instances {
		if i.HasRuntime() {
			return true
		}
	}
	return false
}

// AnyConfig reports whether a config_file is configured for at least one instance.
func (r *Registry) AnyConfig() bool {
	for _, i := range r.instances {
		if i.ConfigFile != "" {
			return true
		}
	}
	return false
}

// AnyStats reports whether the stats page is configured for at least one instance.
func (r *Registry) AnyStats() bool {
	for _, i := range r.instances {
		if i.HasStats() {
			return true
		}
	}
	return false
}

// Counts returns how many instances are configured and how many of them are available.
func (r *Registry) Counts() (total, available int) {
	for _, i := range r.instances {
		total++
		if i.Available() {
			available++
		}
	}
	return total, available
}

// ConnectAll tries to connect every unavailable instance concurrently and returns how many are
// available afterwards.
func (r *Registry) ConnectAll() int {
	var wg sync.WaitGroup
	for _, inst := range r.instances {
		if inst.Available() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := inst.TryConnect(); err != nil {
				slog.Warn("HAProxy instance unavailable", "instance", inst.Name, "error", err)
			} else {
				slog.Info("HAProxy instance available", "instance", inst.Name)
			}
		}()
	}
	wg.Wait()

	n := 0
	for _, inst := range r.instances {
		if inst.Available() {
			n++
		}
	}
	return n
}

// Retry reconnects the unavailable instances every interval until all are available or ctx is done.
// It blocks; run it in a goroutine.
func (r *Registry) Retry(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var down []*Instance
		for _, inst := range r.instances {
			if !inst.Available() {
				down = append(down, inst)
			}
		}
		if len(down) == 0 {
			slog.Info("All HAProxy instances are available, stopping reconnection attempts")
			return
		}
		var wg sync.WaitGroup
		for _, inst := range down {
			wg.Add(1)
			go func() {
				defer wg.Done()
				before := inst.LastError()
				if err := inst.TryConnect(); err != nil {
					// A failure that repeats unchanged is not news; a different reason is.
					if inst.LastError() != before {
						slog.Warn("HAProxy instance still unavailable, with a different error", "instance", inst.Name, "error", err)
					} else {
						slog.Debug("HAProxy instance still unavailable", "instance", inst.Name, "error", err)
					}
				} else {
					slog.Info("HAProxy instance is now available", "instance", inst.Name)
				}
			}()
		}
		wg.Wait()
	}
}

// Instance statuses reported by Describe.
const (
	StatusAvailable   = "available"
	StatusUnavailable = "unavailable"
)

// InstanceInfo is the public description of an instance, safe to expose to MCP clients: it never
// contains credentials.
type InstanceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"` // StatusAvailable or StatusUnavailable
	// ConfigFile is set when the haproxy.cfg of the instance is available, which enables bind IPs and
	// frontend-to-backend routes in list_frontends, list_backends and list_services.
	ConfigFile string `json:"configFile,omitempty"`
	// Error is why an unavailable instance could not be reached (last attempt).
	Error       string     `json:"error,omitempty"`
	LastAttempt *time.Time `json:"lastAttempt,omitempty"`
	Attempts    int        `json:"attempts,omitempty"`
	// Capabilities are the configured APIs: "runtime" (read/write) and/or "stats" (read-only). They
	// are reported for unavailable instances too.
	Capabilities []string `json:"capabilities"`
	// Addresses are the IPs (or names, if the configuration uses names) HAProxy is reachable at:
	// the configured "address" setting, else the hosts of the Runtime API (tcp) and stats page URLs,
	// without ports, paths or credentials. Frontends that bind to all addresses ("*:443") are
	// reachable on these. An instance reached only through a unix socket has none unless configured.
	Addresses []string `json:"addresses,omitempty"`
}

// EffectiveAddresses returns the addresses an instance is reachable at: the configured ones, else
// the hosts of its Runtime API and stats page URLs.
func EffectiveAddresses(configured []string, runtimeURL, statsURL string) []string {
	if len(configured) > 0 {
		return configured
	}
	return hosts(runtimeURL, statsURL)
}

// hosts returns the distinct hosts of the given URLs, in order; URLs without a host (unix sockets) and
// unparsable ones are skipped.
func hosts(urls ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || seen[u.Hostname()] {
			continue
		}
		seen[u.Hostname()] = true
		out = append(out, u.Hostname())
	}
	return out
}

// Describe returns the public description of every instance, sorted by name.
func (r *Registry) Describe() []InstanceInfo {
	infos := make([]InstanceInfo, 0, len(r.instances))
	for _, inst := range r.instances {
		info := InstanceInfo{Name: inst.Name, Description: inst.Description, ConfigFile: inst.ConfigFile, Capabilities: []string{}}

		inst.mu.RLock()
		info.Status = StatusUnavailable
		if inst.client != nil {
			info.Status = StatusAvailable
		} else {
			info.Error = inst.lastErr
		}
		if inst.attempts > 0 {
			t := inst.lastAttempt
			info.LastAttempt, info.Attempts = &t, inst.attempts
		}
		inst.mu.RUnlock()

		if inst.HasRuntime() {
			info.Capabilities = append(info.Capabilities, "runtime")
		}
		if inst.HasStats() {
			info.Capabilities = append(info.Capabilities, "stats")
		}
		info.Addresses = EffectiveAddresses(inst.Addresses, inst.RuntimeURL, inst.StatsURL)
		infos = append(infos, info)
	}
	return infos
}
