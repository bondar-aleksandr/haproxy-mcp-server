package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy"
	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy/hacfg"
)

func registerConfigTool(s *server.MCPServer, reg *haproxy.Registry) {
	slog.Info("Registering HAProxy config tool...")

	showConfig := mcp.NewTool("show_config",
		readOnly(),
		mcp.WithDescription("The raw haproxy.cfg of the instance, exactly as written (comments included), with values that look like "+
			"credentials masked as <redacted>. The escape hatch for what the structured tools do not show: redirects, deny/return rules, "+
			"timeouts, server options, http-check, caching, global settings. Prefer list_services / list_frontends / list_backends first. "+
			"Optional filter, like show_stat: only the sections whose header contains it (\"frontend web_FE\", \"backend app_BE\", "+
			"\"global\", \"defaults\"; \"defaults\" settings apply to the sections after them). The first result is a JSON summary "+
			"(file, modification time, line ranges of the returned sections, warnings), the second the configuration text. "+
			"The file is a copy of the configuration: check the warnings, it may not be what HAProxy is running."),
		mcp.WithString("filter", mcp.Description("Only the sections whose header (e.g. \"frontend web_FE\") contains this text")),
	)
	addConfigTool(s, reg, showConfig, func(ctx context.Context, req mcp.CallToolRequest, inst *haproxy.Instance) (*mcp.CallToolResult, error) {
		filter := getString(req, "filter")
		slog.InfoContext(ctx, "Executing show_config", "filter", filter)
		dump, err := inst.ShowConfig(filter)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to show config", "error", err)
			return mcp.NewToolResultError("Failed to show config: " + err.Error()), nil
		}
		summary := struct {
			Instance string             `json:"instance"`
			File     string             `json:"config_file"`
			Modified time.Time          `json:"modified"`
			Lines    int                `json:"lines"`
			Sections []hacfg.SectionRef `json:"sections,omitempty"`
			Redacted int                `json:"redacted_values"`
			Warnings []string           `json:"warnings,omitempty"`
		}{inst.Name, dump.File, dump.Modified.UTC(), dump.Lines, dump.Sections, dump.Redacted, dump.Warnings}
		meta, err := json.Marshal(summary)
		if err != nil {
			return mcp.NewToolResultError("Internal server error: failed to marshal the summary"), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(meta)), mcp.NewTextContent(dump.Text)}}, nil
	})

	slog.Info("Config tool registered")
}
