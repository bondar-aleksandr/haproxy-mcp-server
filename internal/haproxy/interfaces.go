package haproxy

import (
	"context"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy/common"
	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy/stats"
)

// RuntimeClient defines the interface for interacting with HAProxy's Runtime API
type RuntimeClient interface {
	// Runtime API operations
	ExecuteRuntimeCommand(command string) (string, error)
	ExecuteRuntimeCommandWithContext(ctx context.Context, command string) (string, error)
	GetProcessInfo() (map[string]string, error)
	GetProcessInfoWithContext(ctx context.Context) (map[string]string, error)
	Close() error
}

// StatsClient defines the interface for interacting with HAProxy's Stats API
type StatsClient interface {
	// Stats API operations
	GetStats() (*stats.HAProxyStats, error)
	GetSchema() (*stats.StatsSchema, error)

	// Data filtering operations
	FilterStats(stats *stats.HAProxyStats, proxyName, serviceName string) []common.StatItem
	GetFrontends(stats *stats.HAProxyStats) []common.StatItem
	GetBackends(stats *stats.HAProxyStats) []common.StatItem
	GetServers(stats *stats.HAProxyStats) []common.StatItem
	GetServersByBackend(stats *stats.HAProxyStats, backendName string) []common.StatItem
}
