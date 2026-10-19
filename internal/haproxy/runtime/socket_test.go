package haproxy

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeRuntime serves the Runtime API over TCP: one command per connection, response, close.
func fakeRuntime(t *testing.T, respond func(conn net.Conn, command string)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				cmd, _ := bufio.NewReader(conn).ReadString('\n')
				respond(conn, strings.TrimSpace(cmd))
			}()
		}
	}()
	return "tcp://" + ln.Addr().String()
}

// A large response delivered in small, delayed segments must be read completely.
func TestExecuteCommandReadsWholeResponse(t *testing.T) {
	want := strings.Repeat("0123456789", 1500) // 15 KB, more than a read buffer
	url := fakeRuntime(t, func(conn net.Conn, cmd string) {
		if cmd != "show stat" {
			_, _ = conn.Write([]byte("Name: HAProxy\n"))
			return
		}
		for i := 0; i < len(want); i += 700 {
			_, _ = conn.Write([]byte(want[i:min(i+700, len(want))]))
			time.Sleep(15 * time.Millisecond)
		}
	})

	c, err := NewHAProxyClient(url)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ExecuteRuntimeCommand("show stat")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("response truncated: got %d bytes, want %d", len(got), len(want))
	}
}

// HAProxy answers a command the socket's access level does not allow with plain text.
func TestRefusedCommandIsAnError(t *testing.T) {
	url := fakeRuntime(t, func(conn net.Conn, cmd string) {
		switch cmd {
		case "show fd":
			_, _ = conn.Write([]byte("Permission denied\n\n"))
		case "show nothing":
			_, _ = conn.Write([]byte("Unknown command: 'show', but maybe one of the following ones is a better match:\n  show info\n"))
		default:
			_, _ = conn.Write([]byte("Name: HAProxy\n"))
		}
	})
	c, err := NewHAProxyClient(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExecuteRuntimeCommand("show fd"); err == nil || !strings.Contains(err.Error(), "Permission denied") || !strings.Contains(err.Error(), "access level") {
		t.Errorf("Permission denied should be an error, got %v", err)
	}
	if _, err := c.ExecuteRuntimeCommand("show nothing"); err == nil || !strings.Contains(err.Error(), "Unknown command") {
		t.Errorf("Unknown command should be an error, got %v", err)
	}
	if out, err := c.ExecuteRuntimeCommand("show info"); err != nil || !strings.HasPrefix(out, "Name: HAProxy") {
		t.Errorf("a normal response must pass: %q, %v", out, err)
	}
}

func TestCleanSocatStderr(t *testing.T) {
	in := "2026/10/19 12:34:17 socat[1154023] E connect(5, AF=2 127.0.0.1:1, 16): Connection refused\n"
	again := "2026/10/19 12:35:47 socat[1160002] E connect(5, AF=2 127.0.0.1:1, 16): Connection refused\n"
	want := "E connect(5, AF=2 127.0.0.1:1, 16): Connection refused"
	if got := cleanSocatStderr(in); got != want || cleanSocatStderr(again) != want {
		t.Errorf("got %q and %q, want %q for both", cleanSocatStderr(in), cleanSocatStderr(again), want)
	}
	if got := cleanSocatStderr("two\nlines\n"); got != "two\nlines" {
		t.Errorf("other text must be kept: %q", got)
	}
}
