package mcp

import (
    "context"
    "log/slog"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"

    "github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy"
)

func registerStatTools(s *server.MCPServer, reg *haproxy.Registry) {
    slog.Info("Registering HAProxy statistics & process info tools...")

    // show_stat tool
    showStat := mcp.NewTool("show_stat",
        readOnly(),
        mcp.WithDescription("Shows HAProxy statistics table (show stat command)"),
        mcp.WithString("filter", mcp.Description("Optional filter for proxy or server names")),
    )
    addAnyTool(s, reg, showStat, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
        filter := getString(req, "filter")
        slog.InfoContext(ctx, "Executing show_stat", "filter", filter)
        return callJSON(ctx, "get statistics", "stats", func() (interface{}, error) {
            return client.ShowStat(filter)
        })
    })

    // show_info tool
    showInfo := mcp.NewTool("show_info",
        readOnly(),
        mcp.WithDescription("Shows HAProxy runtime information (version, uptime, limits, mode)"),
    )
    addRuntimeTool(s, reg, showInfo, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
        slog.InfoContext(ctx, "Executing show_info")
        return callJSON(ctx, "get runtime info", "info", func() (interface{}, error) {
            return client.GetRuntimeInfo()
        })
    })

    // debug_counters tool
    debugCounters := mcp.NewTool("debug_counters",
        readOnly(),
        mcp.WithDescription("Lists HAProxy's internal rare-event counters (\"debug counters\"): only those that are not zero, so an empty list is the healthy case. Needs an operator-level (or higher) Runtime API socket."),
    )
    addRuntimeTool(s, reg, debugCounters, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
        slog.InfoContext(ctx, "Executing debug_counters")
        return callJSON(ctx, "get debug counters", "counters", func() (interface{}, error) {
            return client.DebugCounters()
        })
    })

    // clear_counters_all tool
    clearAll := mcp.NewTool("clear_counters_all",
        closedWorld(),
        mcp.WithDescription("Reset all HAProxy statistics counters"),
    )
    addRuntimeTool(s, reg, clearAll, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
        slog.InfoContext(ctx, "Executing clear_counters_all")
        return callExec(ctx, "clear counters", func() (string, error) {
            if err := client.ClearCountersAll(); err != nil {
                return "", err
            }
            return "All statistics counters have been reset successfully", nil
        })
    })

    slog.Info("Statistic & process info tools registered")
}