package mcp

import (
	"context"
	"log/slog"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// overviewResult runs an overview call with the "detail" argument of the request.
func overviewResult(ctx context.Context, req mcp.CallToolRequest, action, key string,
	fn func(detail haproxy.Detail) (interface{}, []string, error)) (*mcp.CallToolResult, error) {
	detail, err := haproxy.ParseDetail(getString(req, "detail"))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return callJSONMap(ctx, action, func() (map[string]interface{}, error) {
		items, warnings, err := fn(detail)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{key: items, "warnings": warnings}, nil
	})
}

func registerFrontendTools(s *server.MCPServer, reg *haproxy.Registry) {
	slog.Info("Registering HAProxy frontend tools...")

	// list_services tool
	listServices := mcp.NewTool("list_services",
		readOnly(),
		mcp.WithDescription("START HERE. Lists what HAProxy serves: for each frontend, where it listens (bind addresses) and which backends "+
			"it routes to (conditions with the definitions of their ACLs, e.g. which Host goes to which backend; default), with each backend's status and servers up/total. Needs the instance's config_file for "+
			"routes and bind IPs. Use list_frontends / list_backends to look closer at one side."),
		mcp.WithString("name", mcp.Description("Only this frontend")),
		withDetail("terse", "standard"),
	)
	addAnyTool(s, reg, listServices, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
		name := getString(req, "name")
		slog.InfoContext(ctx, "Executing list_services", "name", name)
		return overviewResult(ctx, req, "list services", "services", func(d haproxy.Detail) (interface{}, []string, error) {
			return client.ListServices(name, d)
		})
	})

	// list_frontends tool
	listFrontends := mcp.NewTool("list_frontends",
		readOnly(),
		mcp.WithDescription("Lists HAProxy frontends: status (OPEN, STOP, ...), where they listen (binds, TLS) and the backends they route to. "+
			"terse: iid, name, status, binds, backends. standard: adds counters (sessions, rates, bytes, errors) and the routes with conditions "+
			"and ACLs. full: adds every stats column. Bind IPs and routes need the instance's config_file; without it only listening ports are known."),
		mcp.WithString("name", mcp.Description("Only this frontend")),
		withDetail("terse", "standard", "full"),
	)
	addAnyTool(s, reg, listFrontends, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
		name := getString(req, "name")
		slog.InfoContext(ctx, "Executing list_frontends", "name", name)
		return overviewResult(ctx, req, "list frontends", "frontends", func(d haproxy.Detail) (interface{}, []string, error) {
			return client.ListFrontends(name, d)
		})
	})

	slog.Info("Frontend tools registered")
}
