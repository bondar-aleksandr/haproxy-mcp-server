package haproxy

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry(t *testing.T) {
	if _, err := NewRegistry(); err == nil {
		t.Error("empty registry should fail")
	}
	if _, err := NewRegistry(&Instance{Name: "a"}, &Instance{Name: "a"}); err == nil {
		t.Error("duplicate names should fail")
	}

	reg, err := NewRegistry(&Instance{Name: "b"}, &Instance{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(reg.Names(), ","); got != "a,b" {
		t.Errorf("names = %s, want sorted a,b", got)
	}
	if _, err := reg.Resolve(""); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("empty name with 2 instances should fail listing names: %v", err)
	}
	if inst, err := reg.Resolve("b"); err != nil || inst.Name != "b" {
		t.Errorf("Resolve(b) = %v, %v", inst, err)
	}
	if _, err := reg.Resolve("c"); err == nil {
		t.Error("unknown name should fail")
	}

	single, _ := NewRegistry(&Instance{Name: "only"})
	if inst, err := single.Resolve(""); err != nil || inst.Name != "only" {
		t.Errorf("single instance should be the default: %v, %v", inst, err)
	}
}

func TestRetryBringsInstanceUp(t *testing.T) {
	var calls atomic.Int32
	flaky := &Instance{Name: "flaky", StatsURL: "http://u:secret@h/stats", Connect: func() (*HAProxyClient, error) {
		if calls.Add(1) < 3 {
			return nil, errors.New("connection refused to http://u:secret@h/stats")
		}
		return &HAProxyClient{}, nil
	}}
	solid := &Instance{Name: "solid", Connect: func() (*HAProxyClient, error) { return &HAProxyClient{}, nil }}
	reg, _ := NewRegistry(flaky, solid)

	if n := reg.ConnectAll(); n != 1 || flaky.Available() || !solid.Available() {
		t.Fatalf("after first attempt: available=%d flaky=%v", n, flaky.Available())
	}
	if _, err := flaky.Client(); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("expected a scrubbed unavailable error, got %v", err)
	}
	info := reg.Describe()[0]
	if info.Status != StatusUnavailable || info.Attempts != 1 || info.LastAttempt == nil || info.Error == "" {
		t.Errorf("unexpected info for unavailable instance: %+v", info)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { reg.Retry(ctx, 10*time.Millisecond); close(done) }()

	select {
	case <-done: // Retry returns by itself once everything is available
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not bring the instance up")
	}
	if !flaky.Available() || calls.Load() != 3 {
		t.Errorf("flaky available=%v after %d calls", flaky.Available(), calls.Load())
	}
	if info := reg.Describe()[0]; info.Status != StatusAvailable || info.Error != "" {
		t.Errorf("unexpected info after recovery: %+v", info)
	}
}

func TestRetryStopsOnCancel(t *testing.T) {
	down := &Instance{Name: "down", Connect: func() (*HAProxyClient, error) { return nil, errors.New("no") }}
	reg, _ := NewRegistry(down)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { reg.Retry(ctx, 5*time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Retry did not stop after cancellation")
	}
}
