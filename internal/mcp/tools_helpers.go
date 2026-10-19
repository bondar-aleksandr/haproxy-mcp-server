package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    "github.com/mark3labs/mcp-go/mcp"
)

// callJSON handles executing a client call, error logging, and JSON marshalling
func callJSON(ctx context.Context, action, mapKey string, fn func() (interface{}, error)) (*mcp.CallToolResult, error) {
    v, err := fn()
    if err != nil {
        slog.ErrorContext(ctx, "Failed to "+action, "error", err)
        return mcp.NewToolResultError(fmt.Sprintf("Failed to %s: %v", action, err)), nil
    }
    out, err := json.Marshal(map[string]interface{}{mapKey: v})
    if err != nil {
        slog.ErrorContext(ctx, "Failed to marshal "+mapKey+" output", "error", err)
        return mcp.NewToolResultError("Internal server error: failed to marshal results"), nil
    }
    return mcp.NewToolResultText(string(out)), nil
}

// callJSONMap is callJSON for results with several top-level keys; empty "warnings" are left out.
func callJSONMap(ctx context.Context, action string, fn func() (map[string]interface{}, error)) (*mcp.CallToolResult, error) {
    m, err := fn()
    if err != nil {
        slog.ErrorContext(ctx, "Failed to "+action, "error", err)
        return mcp.NewToolResultError(fmt.Sprintf("Failed to %s: %v", action, err)), nil
    }
    if w, ok := m["warnings"].([]string); ok && len(w) == 0 {
        delete(m, "warnings")
    }
    out, err := json.Marshal(m)
    if err != nil {
        slog.ErrorContext(ctx, "Failed to marshal "+action+" output", "error", err)
        return mcp.NewToolResultError("Internal server error: failed to marshal results"), nil
    }
    return mcp.NewToolResultText(string(out)), nil
}

// callExec handles executing a client action, error logging, and returning plain text
func callExec(ctx context.Context, action string, fn func() (string, error)) (*mcp.CallToolResult, error) {
    s, err := fn()
    if err != nil {
        slog.ErrorContext(ctx, "Failed to "+action, "error", err)
        return mcp.NewToolResultError(fmt.Sprintf("Failed to %s: %v", action, err)), nil
    }
    return mcp.NewToolResultText(s), nil
}

// getString extracts a string argument from the request
func getString(req mcp.CallToolRequest, key string) string {
    v, err := req.RequireString(key)
    if err != nil {
        return ""
    }
    return v
}

// withDetail adds the "detail" argument of the overview tools. levels lists the accepted values.
func withDetail(levels ...string) mcp.ToolOption {
    return mcp.WithString("detail",
        mcp.Enum(levels...),
        mcp.Description("How much to report: terse (default) is the first look, standard adds counters, servers and routes, "+
            "full adds every column HAProxy reports. Start with terse, then narrow with the name argument before asking for more."),
    )
}
