package mcp

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy"
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
)

// instanceParam is the name of the tool argument that selects the HAProxy instance.
const instanceParam = "instance"

// RegisterTools registers the MCP tools and resources.
//
// Every tool takes an "instance" argument selecting the HAProxy instance to act on (optional when
// only one is configured). Which tools exist depends on the configured APIs, not on which instances
// are currently reachable: tools that need the Runtime API (tcp4/unix mode) are only registered when
// at least one instance has it configured, and fail for instances that do not; when no instance has
// it, only the read-only tools that the stats data can serve (show_stat, list/get backends and
// servers) are offered. A tool call fails while its instance is unavailable.
func RegisterTools(s *server.MCPServer, reg *haproxy.Registry) {
    slog.Info("Registering HAProxy MCP tools...", "instances", reg.Names(), "runtime_api", reg.AnyRuntime(), "stats_api", reg.AnyStats())
    registerInstancesResource(s, reg)
    registerStatTools(s, reg)
    registerConfigTool(s, reg)
    registerFrontendTools(s, reg)
    registerBackendTools(s, reg)
    slog.Info("All HAProxy MCP tools registered successfully")
}

// clientHandler is a tool handler that receives the HAProxy client of the selected instance.
type clientHandler func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error)

// withInstanceParam adds the "instance" argument to a tool.
func withInstanceParam(tool mcp.Tool, reg *haproxy.Registry) mcp.Tool {
    opts := []mcp.PropertyOption{
        mcp.Description("HAProxy instance to operate on; see the " + instancesResourceURI + " resource"),
        mcp.Enum(reg.Names()...),
    }
    if len(reg.Names()) > 1 {
        opts = append(opts, mcp.Required())
    }
    mcp.WithString(instanceParam, opts...)(&tool)
    return tool
}

// addTool registers a tool for the instance selected by its "instance" argument. needRuntime tools
// fail for instances without a Runtime API.
func addTool(s *server.MCPServer, reg *haproxy.Registry, tool mcp.Tool, needRuntime bool, h clientHandler) {
    s.AddTool(withInstanceParam(tool, reg), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        inst, err := reg.Resolve(getString(req, instanceParam))
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        if needRuntime && !inst.HasRuntime() {
            return mcp.NewToolResultError(fmt.Sprintf("tool %s is not available for instance %q: it has no Runtime API configured (stats page only)", req.Params.Name, inst.Name)), nil
        }
        client, err := inst.Client()
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        slog.InfoContext(ctx, "Selected HAProxy instance", "instance", inst.Name, "tool", req.Params.Name)
        return h(ctx, req, client)
    })
}

// configHandler is a tool handler that works on the config_file of the selected instance.
type configHandler func(ctx context.Context, req mcp.CallToolRequest, inst *haproxy.Instance) (*mcp.CallToolResult, error)

// addConfigTool registers a tool that reads the instance's config_file. It needs no connection to
// HAProxy, so it works for an instance that is currently unavailable, and fails for instances that
// have no config_file. It is not registered while no instance has one.
func addConfigTool(s *server.MCPServer, reg *haproxy.Registry, tool mcp.Tool, h configHandler) {
    if !reg.AnyConfig() {
        slog.Info("No instance has a config_file, skipping tool", "tool", tool.Name)
        return
    }
    s.AddTool(withInstanceParam(tool, reg), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        inst, err := reg.Resolve(getString(req, instanceParam))
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        if inst.ConfigFile == "" {
            return mcp.NewToolResultError(fmt.Sprintf("tool %s is not available for instance %q: it has no config_file configured", req.Params.Name, inst.Name)), nil
        }
        slog.InfoContext(ctx, "Selected HAProxy instance", "instance", inst.Name, "tool", req.Params.Name)
        return h(ctx, req, inst)
    })
}

// addRuntimeTool registers a tool that can only work through the Runtime API.
func addRuntimeTool(s *server.MCPServer, reg *haproxy.Registry, tool mcp.Tool, h clientHandler) {
    if !reg.AnyRuntime() {
        slog.Info("Runtime API not configured on any instance, skipping tool", "tool", tool.Name)
        return
    }
    addTool(s, reg, tool, true, h)
}

// addAnyTool registers a tool that works with either the Runtime API or the stats page.
func addAnyTool(s *server.MCPServer, reg *haproxy.Registry, tool mcp.Tool, h clientHandler) {
    addTool(s, reg, tool, false, h)
}

// readOnly marks a tool as not modifying anything. Without it MCP clients assume the default
// annotations (destructive, open-world).
func readOnly() mcp.ToolOption {
    return func(t *mcp.Tool) {
        mcp.WithReadOnlyHintAnnotation(true)(t)
        mcp.WithDestructiveHintAnnotation(false)(t)
        mcp.WithIdempotentHintAnnotation(true)(t)
        mcp.WithOpenWorldHintAnnotation(false)(t)
    }
}

// closedWorld marks a tool as acting only on the configured HAProxy instance, not on an open set of
// external entities.
func closedWorld() mcp.ToolOption {
    return mcp.WithOpenWorldHintAnnotation(false)
}
