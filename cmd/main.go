package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server" // Import directly without alias

	"github.com/tuannvm/haproxy-mcp-server/internal/config"
	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy"
	"github.com/tuannvm/haproxy-mcp-server/internal/mcp"
)

func main() {
	// --- Configuration ---
	configPath := flag.String("config", "", "path to the TOML configuration file defining the HAProxy instances (required; environment variables override its values)")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// --- Logging ---
	// Configure logging level
	var logLevel slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		slog.Warn("Invalid log level, defaulting to 'info'", "configured_level", cfg.LogLevel)
		logLevel = slog.LevelInfo
	}

	// Use text handler for development/stdio mode
	var handler slog.Handler
	if cfg.MCPTransport == "stdio" || os.Getenv("PRETTY_LOG") == "true" {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logLevel,
		})
	} else {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: logLevel,
		})
	}
	slog.SetDefault(slog.New(handler))

	slog.Info("Starting HAProxy MCP Server...")
	if *configPath != "" {
		slog.Info("Using configuration file", "path", *configPath)
	}
	slog.Info("Loaded configuration", "config", cfg)

	// --- Context and Shutdown Handling ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- HAProxy instances ---
	registry, err := newRegistry(cfg.Instances)
	if err != nil {
		slog.Error("Failed to set up HAProxy instances", "error", err)
		os.Exit(1)
	}
	// Instances that cannot be reached now are listed as unavailable and retried in the background.
	if n := registry.ConnectAll(); n < len(cfg.Instances) {
		slog.Warn("Some HAProxy instances are unavailable", "available", n, "configured", len(cfg.Instances),
			"retry_interval", cfg.RetryInterval)
		if cfg.RetryInterval > 0 {
			go registry.Retry(ctx, cfg.RetryInterval)
		}
	}

	// --- MCP Server ---
	// Create MCP Server with name and version
	mcpServer := server.NewMCPServer("haproxy-mcp-server", "0.1.0")

	// --- Register Tools ---
	mcp.RegisterTools(mcpServer, registry) // Use mcp.RegisterTools instead of tools.RegisterTools

	// --- Transport Handling ---
	switch cfg.MCPTransport {
	case "stdio":
		slog.Info("Running MCP server in stdio mode")
		// Use ServeStdio for stdio mode
		if err := server.ServeStdio(mcpServer); err != nil {
			slog.Error("MCP server exited with error (stdio)", "error", err)
			os.Exit(1)
		}
		slog.Info("MCP server (stdio) finished gracefully")

	case "http":
		addr := fmt.Sprintf(":%d", cfg.MCPPort)

		// Create an SSE server
		mcp_handler :=server.NewStreamableHTTPServer(mcpServer,
			server.WithEndpointPath("/mcp"),
		)

		// Create HTTP server with SSE handler
		httpServer := &http.Server{
			Addr:    addr,
			Handler: mcp_handler,
		}

		go func() {
			slog.Info("Starting HTTP server for MCP http transport", "address", addr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP server failed", "error", err)
				os.Exit(1)
			}
		}()

		// Wait for shutdown signal
		<-ctx.Done()
		slog.Info("Shutdown signal received, stopping HTTP server...")

		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("HTTP server graceful shutdown failed", "error", err)
		} else {
			slog.Info("HTTP server stopped gracefully")
		}

	default:
		slog.Error("Invalid MCP_TRANSPORT specified", "transport", cfg.MCPTransport)
		os.Exit(1)
	}

	slog.Info("HAProxy MCP Server stopped.")
}

// newRegistry declares the configured instances. Connecting to them is left to the registry.
func newRegistry(configs []config.InstanceConfig) (*haproxy.Registry, error) {
	instances := make([]*haproxy.Instance, 0, len(configs))
	for _, ic := range configs {
		runtimeAPIURL, err := ic.RuntimeAPIURL()
		if err != nil {
			return nil, fmt.Errorf("instance %q: %w", ic.Name, err)
		}
		statsURL := ic.StatsAPIURL()
		instances = append(instances, &haproxy.Instance{
			Name:        ic.Name,
			Description: ic.Description,
			RuntimeURL:  runtimeAPIURL,
			StatsURL:    statsURL,
			ConfigFile:  ic.ConfigFile,
			Addresses:   ic.Addresses,
			Connect:     func() (*haproxy.HAProxyClient, error) { return connect(ic, runtimeAPIURL, statsURL) },
		})
	}
	return haproxy.NewRegistry(instances...)
}

// connect creates the client of an instance and checks that every configured API answers.
func connect(ic config.InstanceConfig, runtimeAPIURL, statsURL string) (*haproxy.HAProxyClient, error) {
	slog.Info("Connecting to HAProxy", "instance", ic.Name,
		"runtimeAPIURL", config.RedactURL(runtimeAPIURL), "statsURL", config.RedactURL(statsURL))
	client, err := haproxy.NewHAProxyClient(runtimeAPIURL, statsURL)
	if err != nil {
		return nil, err
	}
	if err := client.CheckConnectivity(); err != nil {
		_ = client.Close()
		return nil, err
	}
	client.ConfigFile = ic.ConfigFile
	client.Addresses = haproxy.EffectiveAddresses(ic.Addresses, runtimeAPIURL, statsURL)
	return client, nil
}
