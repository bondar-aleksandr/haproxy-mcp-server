package mcp

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy"
)

func registerBackendTools(s *server.MCPServer, reg *haproxy.Registry) {
	slog.Info("Registering HAProxy backend tools...")

	// list_backends tool
	listBackends := mcp.NewTool("list_backends",
		readOnly(),
		mcp.WithDescription("Lists HAProxy backends and their servers. "+
			"terse: iid, name, status, servers total/up/down/maint, the frontends using it (used_by) and the servers that are not UP. "+
			"standard: adds counters and the table of servers (sid, status, address, weight, check result, operational state, sessions). "+
			"full: adds every stats column of the backend and of each server, and each server's full state (iid/sid match show_stat). "+
			"Use name (and server) to look at one backend or server, e.g. name=app_BE server=app1 for that server's details."),
		mcp.WithString("name", mcp.Description("Only this backend")),
		mcp.WithString("server", mcp.Description("Only this server of the backend given in name (implies at least standard)")),
		withDetail("terse", "standard", "full"),
	)
	addAnyTool(s, reg, listBackends, func(ctx context.Context, req mcp.CallToolRequest, client *haproxy.HAProxyClient) (*mcp.CallToolResult, error) {
		name, server := getString(req, "name"), getString(req, "server")
		slog.InfoContext(ctx, "Executing list_backends", "name", name, "server", server)
		return overviewResult(ctx, req, "list backends", "backends", func(d haproxy.Detail) (interface{}, []string, error) {
			return client.ListBackends(name, server, d)
		})
	})

	slog.Info("Backend tools registered")
}
