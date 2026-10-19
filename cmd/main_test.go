package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/config"
	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/haproxy"
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
	go func() { done <- serveHTTP(ctx, server.NewMCPServer("test", "0"), testRegistry(t), ln, 10*time.Second) }()

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

func TestPrintVersion(t *testing.T) {
	oldV, oldC, oldB := GitVersion, GitCommit, BuildDate
	defer func() { GitVersion, GitCommit, BuildDate = oldV, oldC, oldB }()
	GitVersion, GitCommit, BuildDate = "v1.2.3", "abc1234", "2026-01-02T03:04:05Z"

	var out strings.Builder
	printVersion(&out)
	for _, want := range []string{"Version: v1.2.3", "Commit: abc1234", "Build Date: 2026-01-02T03:04:05Z", "GoVersion: go", "Platform: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

// testRegistry has two instances: "up" is connected, "down" is not.
func testRegistry(t *testing.T) *haproxy.Registry {
	t.Helper()
	up := &haproxy.Instance{Name: "up", StatsURL: "http://u:secret@10.0.0.1:8404/stats"}
	up.SetClient(&haproxy.HAProxyClient{})
	down := &haproxy.Instance{Name: "down", StatsURL: "http://10.0.0.2:8404/stats", Connect: func() (*haproxy.HAProxyClient, error) {
		return nil, errors.New("dial tcp 10.0.0.2:8404: connection refused")
	}}
	_ = down.TryConnect()
	reg, err := haproxy.NewRegistry(up, down)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestHealthz(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server.NewMCPServer("test", "0"), testRegistry(t), ln, 5*time.Second) }()
	defer func() { stop(); <-done }()
	url := "http://" + ln.Addr().String() + "/healthz"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body := new(strings.Builder)
	_, _ = io.Copy(body, resp.Body)
	_ = resp.Body.Close()
	var got struct {
		Status    string         `json:"status"`
		Instances map[string]int `json:"instances"`
	}
	if err := json.Unmarshal([]byte(body.String()), &got); err != nil {
		t.Fatalf("body %q: %v", body.String(), err)
	}
	// Healthy although one of the two instances is unavailable: this is liveness, not HAProxy's state.
	if resp.StatusCode != http.StatusOK || got.Status != "ok" || got.Instances["total"] != 2 || got.Instances["available"] != 1 {
		t.Errorf("GET /healthz = %d %q", resp.StatusCode, body.String())
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	// Exactly the counts: nothing that identifies an instance (name, URL, error) is given out.
	if want := `{"instances":{"available":1,"total":2},"status":"ok"}` + "\n"; body.String() != want {
		t.Errorf("body %q, want %q", body.String(), want)
	}

	head, err := http.Head(url)
	if err != nil || head.StatusCode != http.StatusOK {
		t.Errorf("HEAD /healthz = %v %v", head, err)
	}
	post, err := http.Post(url, "text/plain", nil)
	if err != nil || post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %v %v", post, err)
	}
}
