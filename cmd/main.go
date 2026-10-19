package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
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
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
		slog.Info("Starting HTTP server for MCP http transport", "address", addr)
		if err := serveHTTP(ctx, mcpServer, ln, 5*time.Second); err != nil {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
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
			Connect: func() func() (*haproxy.HAProxyClient, error) {
				attempts := 0
				return func() (*haproxy.HAProxyClient, error) {
					attempts++
					return connect(ic, runtimeAPIURL, statsURL, attempts == 1)
				}
			}(),
		})
	}
	return haproxy.NewRegistry(instances...)
}

// connect creates the client of an instance and checks that every configured API answers.
// The first attempt is logged at info level; the retries of an unavailable instance at debug level.
func connect(ic config.InstanceConfig, runtimeAPIURL, statsURL string, first bool) (*haproxy.HAProxyClient, error) {
	level := slog.LevelDebug
	if first {
		level = slog.LevelInfo
	}
	slog.Log(context.Background(), level, "Connecting to HAProxy", "instance", ic.Name,
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

// serveHTTP serves the MCP server over HTTP at /mcp on ln until ctx is done, then shuts down within
// grace. A request still running after grace is cut off (logged, not an error).
func serveHTTP(ctx context.Context, mcpServer *server.MCPServer, ln net.Listener, grace time.Duration) error {
	// The MCP endpoint, served by our own http.Server so that its timeouts and shutdown are ours.
	mux := http.NewServeMux()
	httpServer := &http.Server{Handler: mux}
	mcpHandler := server.NewStreamableHTTPServer(mcpServer,
		server.WithEndpointPath("/mcp"),
		server.WithStreamableHTTPServer(httpServer), // lets mcpHandler.Shutdown stop it
	)
	mux.Handle("/mcp", mcpHandler)

	failed := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	slog.Info("Shutdown signal received, stopping HTTP server...")

	// MCP clients keep a GET event stream open for server notifications. http.Server.Shutdown waits
	// for such a connection to become idle, which it never does while the client is connected, so
	// the sessions have to be closed first: mcpHandler.Shutdown does that and then shuts down
	// httpServer, letting in-flight requests finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := mcpHandler.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP server did not stop in time (a request is still running), closing the remaining connections", "error", err)
		_ = httpServer.Close()
		return nil
	}
	slog.Info("HTTP server stopped gracefully")
	return nil
}
