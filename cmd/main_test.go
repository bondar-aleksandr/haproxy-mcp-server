package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/tuannvm/haproxy-mcp-server/internal/config"
)

func TestNewRegistryKeepsUnreachableInstances(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer up.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close() // nothing listens there any more

	reg, err := newRegistry([]config.InstanceConfig{
		{Name: "bad", StatsEnabled: true, StatsURL: down.URL + "/stats"},
		{Name: "good", StatsEnabled: true, StatsURL: up.URL + "/stats"},
		{Name: "bad-runtime", RuntimeMode: "tcp4", Host: "127.0.0.1", Port: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := reg.ConnectAll(); n != 1 {
		t.Errorf("expected 1 available instance, got %d", n)
	}
	status := map[string]string{}
	for _, info := range reg.Describe() {
		status[info.Name] = info.Status
	}
	want := map[string]string{"good": "available", "bad": "unavailable", "bad-runtime": "unavailable"}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("%s: status %q, want %q (all: %v)", name, status[name], st, status)
		}
	}

	// Config errors are not retried: they fail the setup.
	if _, err := newRegistry([]config.InstanceConfig{{Name: "x", RuntimeMode: "tcp6"}}); err == nil {
		t.Error("expected an error for an invalid runtime mode")
	}
}

// A connected MCP client keeps a GET event stream open. Shutting down must not wait for it: before
// the sessions were closed first, http.Server.Shutdown waited out the whole grace period and failed
// with "context deadline exceeded".
func TestServeHTTPShutdownWithOpenStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server.NewMCPServer("test", "0"), ln, 10*time.Second) }()

	url := "http://" + ln.Addr().String() + "/mcp"
	post := func(session, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	session := resp.Header.Get("Mcp-Session-Id")
	_ = resp.Body.Close()
	if session == "" {
		t.Fatal("no session id")
	}
	post(session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`).Body.Close()

	// The standing GET stream.
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("GET stream: %v %v", stream, err)
	}
	defer stream.Body.Close()
	time.Sleep(100 * time.Millisecond) // let the handler settle into waiting for events

	start := time.Now()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveHTTP returned %v", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("shutdown took %v with a stream open: it waited for the grace period", took)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("shutdown did not finish: an open event stream blocks it")
	}
}
