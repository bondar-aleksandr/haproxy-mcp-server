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
