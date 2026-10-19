package haproxy

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/config"
	runtimeclient "github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy/runtime"
	statsclient "github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy/stats"
)

// HAProxyClient is a combined client that can interact with HAProxy through both runtime API and stats API
type HAProxyClient struct {
	RuntimeClient RuntimeClient
	StatsClient   StatsClient
	StatsURL      string
	RuntimeURL    string

	// ConfigFile is the HAProxy configuration (file or directory of *.cfg), readable by this process,
	// used to tell where frontends listen and which backends they route to.
	ConfigFile string
	cfgCache   cachedConfig

	// Addresses are what the instance is reachable at (see EffectiveAddresses): the endpoints of its
	// wildcard binds.
	Addresses []string
}

// HasRuntime reports whether the Runtime API (tcp4/unix) is configured.
func (c *HAProxyClient) HasRuntime() bool { return c.RuntimeClient != nil }

// HasStats reports whether the stats page API is configured.
func (c *HAProxyClient) HasStats() bool { return c.StatsClient != nil }

// ensureRuntime verifies the runtime client is initialized.
func (c *HAProxyClient) ensureRuntime() error {
	if c.RuntimeClient == nil {
		return fmt.Errorf("runtime client is not initialized")
	}
	return nil
}





// NewHAProxyClient creates a new HAProxy client using the provided configurations
func NewHAProxyClient(runtimeAPIURL string, statsURL string) (*HAProxyClient, error) {
	client := &HAProxyClient{
		StatsURL:   statsURL,
		RuntimeURL: runtimeAPIURL,
	}

	// Initialize runtime client if URL is provided
	if runtimeAPIURL != "" {
		slog.Debug("Initializing HAProxy Runtime API client", "url", config.RedactURL(runtimeAPIURL))
		runtimeClient, err := runtimeclient.NewHAProxyClient(runtimeAPIURL)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize HAProxy Runtime API client: %w", err)
		}
		client.RuntimeClient = runtimeClient
		slog.Debug("HAProxy Runtime API client initialized successfully")
	}

	// Initialize stats client if URL is provided
	if statsURL != "" {
		slog.Debug("Initializing HAProxy Stats client", "url", config.RedactURL(statsURL))
		statsClient, err := statsclient.NewStatsClient(statsURL)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize HAProxy Stats client: %w", err)
		}
		client.StatsClient = statsClient
		slog.Debug("HAProxy Stats client initialized successfully")
	}

	// Ensure at least one client is initialized
	if client.RuntimeClient == nil && client.StatsClient == nil {
		return nil, fmt.Errorf("at least one of Runtime API URL or Stats URL must be provided")
	}

	return client, nil
}

// CheckConnectivity verifies that every configured API answers: the Runtime API is already probed
// when the client is created, so this fetches the stats page.
func (c *HAProxyClient) CheckConnectivity() error {
	if c.StatsClient != nil {
		if _, err := c.StatsClient.GetStats(); err != nil {
			return fmt.Errorf("stats page unreachable: %w", err)
		}
	}
	return nil
}

// Close closes both runtime and stats client connections
func (c *HAProxyClient) Close() error {
	if c.RuntimeClient != nil {
		if err := c.RuntimeClient.Close(); err != nil {
			slog.Error("Error closing runtime client", "error", err)
		}
	}

	return nil
}



// ExecuteRuntimeCommand executes a command on HAProxy's Runtime API
func (c *HAProxyClient) ExecuteRuntimeCommand(command string) (string, error) {
	return c.ExecuteRuntimeCommandWithContext(context.Background(), command)
}

// ExecuteRuntimeCommandWithContext executes a command on HAProxy's Runtime API with context
func (c *HAProxyClient) ExecuteRuntimeCommandWithContext(ctx context.Context, command string) (string, error) {
	if c.RuntimeClient == nil {
		return "", fmt.Errorf("runtime client is not initialized")
	}

	// Use context-aware version if available
	if ctxClient, ok := c.RuntimeClient.(interface {
		ExecuteRuntimeCommandWithContext(ctx context.Context, command string) (string, error)
	}); ok {
		return ctxClient.ExecuteRuntimeCommandWithContext(ctx, command)
	}

	// Fall back to non-context version
	return c.RuntimeClient.ExecuteRuntimeCommand(command)
}

// GetRuntimeInfo retrieves HAProxy process information from runtime API
func (c *HAProxyClient) GetRuntimeInfo() (map[string]string, error) {
    if err := c.ensureRuntime(); err != nil {
        return nil, err
    }
    return c.RuntimeClient.GetProcessInfo()
}

// GetStats retrieves HAProxy statistics from stats page
func (c *HAProxyClient) GetStats() (*statsclient.HAProxyStats, error) {
	if c.StatsClient == nil {
		return nil, fmt.Errorf("stats client is not initialized")
	}
	return c.StatsClient.GetStats()
}

// ShowStat executes the show stat command
func (c *HAProxyClient) ShowStat(filter string) ([]map[string]string, error) {
	// Try stats client first if available
	var statsErr error
	if c.StatsClient != nil {
		var stats *statsclient.HAProxyStats
		stats, statsErr = c.StatsClient.GetStats()
		if statsErr == nil {
			result := []map[string]string{}
			for _, item := range stats.Stats {
				if filter == "" || strings.Contains(item.PxName, filter) || strings.Contains(item.SvName, filter) {
					row := make(map[string]string, len(item.Fields)+4)
					for k, v := range item.Fields {
						row[k] = v
					}
					// Always present, even if the response carried no such fields
					row["pxname"] = item.PxName
					row["svname"] = item.SvName
					row["status"] = item.Status
					row["weight"] = fmt.Sprintf("%d", item.Weight)
					if _, ok := row["type"]; !ok {
						row["type"] = fmt.Sprintf("%d", item.Type)
					}
					result = append(result, row)
				}
			}
			return result, nil
		}
		// Fall back to runtime client if stats client failed
		slog.Warn("Stats client failed, falling back to runtime client", "error", statsErr)
	}

	// Use runtime client as fallback or primary if stats client not available
	if c.RuntimeClient != nil {
		// "show stat" takes numeric <iid> <type> <sid> arguments, not names (anything else is
		// silently ignored), so the filter is applied here.
		response, err := c.RuntimeClient.ExecuteRuntimeCommand("show stat")
		if err != nil {
			return nil, err
		}

		// Parse the CSV output: a "# "-prefixed header line, then one line per frontend, backend, server
		result := []map[string]string{}
		lines := strings.Split(strings.TrimSpace(response), "\n")
		headers := strings.Split(strings.TrimPrefix(lines[0], "# "), ",")
		for _, line := range lines[1:] {
			if line == "" {
				continue
			}
			values := strings.Split(line, ",")
			row := make(map[string]string)
			for j := 0; j < len(headers) && j < len(values); j++ {
				if headers[j] != "" && headers[j] != "-" { // unnamed trailing/placeholder columns
					row[headers[j]] = values[j]
				}
			}
			if filter == "" || strings.Contains(row["pxname"], filter) || strings.Contains(row["svname"], filter) {
				result = append(result, row)
			}
		}
		return result, nil
	}

	// No runtime client to fall back to: report the real reason instead of a generic one
	if statsErr != nil {
		return nil, statsErr
	}
	return nil, fmt.Errorf("neither stats client nor runtime client is initialized")
}

// DebugCounter is a line of "debug counters": how many times a rare event (a BUG_ON/WARN_ON
// condition, a glitch, ...) happened at a code location.
type DebugCounter struct {
	Count    int64  `json:"count"`
	Type     string `json:"type"`     // BUG, CNT, CHK, GLT, ...
	Location string `json:"location"` // file:line function(): "condition" [comment]
}

// DebugCounters returns the counters of "debug counters", which lists those that are not zero (so
// usually none).
func (c *HAProxyClient) DebugCounters() ([]DebugCounter, error) {
	if c.RuntimeClient == nil {
		return nil, fmt.Errorf("runtime client is not initialized")
	}

	response, err := c.RuntimeClient.ExecuteRuntimeCommand("debug counters")
	if err != nil {
		return nil, err
	}

	// The output is a header line ("Count Type Location ...") followed by "<count> <type> <location>"
	counters := []DebugCounter{}
	for _, line := range strings.Split(response, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue // the header
		}
		counters = append(counters, DebugCounter{Count: n, Type: fields[1], Location: strings.Join(fields[2:], " ")})
	}
	return counters, nil
}

// ClearCountersAll clears all counters
func (c *HAProxyClient) ClearCountersAll() error {
	if c.RuntimeClient == nil {
		return fmt.Errorf("runtime client is not initialized")
	}

	_, err := c.RuntimeClient.ExecuteRuntimeCommand("clear counters all")
	return err
}

