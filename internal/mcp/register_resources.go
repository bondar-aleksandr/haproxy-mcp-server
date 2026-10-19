package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy"
)

const instancesResourceURI = "haproxy://instances"

// registerInstancesResource exposes the list of configured HAProxy instances, i.e. the valid values
// of the "instance" tool argument and what each of them supports.
func registerInstancesResource(s *server.MCPServer, reg *haproxy.Registry) {
	resource := mcp.NewResource(instancesResourceURI, "HAProxy instances",
		mcp.WithResourceDescription("HAProxy instances managed by this server: name, description and which APIs "+
			"are configured (\"runtime\": read/write via the Runtime API, \"stats\": read-only via the stats page). "+
			"Pass a name as the \"instance\" argument of the tools."),
		mcp.WithMIMEType("application/json"),
	)
	s.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		slog.InfoContext(ctx, "Reading resource", "uri", instancesResourceURI)
		out, err := json.MarshalIndent(map[string]interface{}{"instances": reg.Describe()}, "", "  ")
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{URI: instancesResourceURI, MIMEType: "application/json", Text: string(out)},
		}, nil
	})
}
