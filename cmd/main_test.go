package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
