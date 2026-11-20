package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/config"
)

// selfSigned writes a certificate for 127.0.0.1 and its key, and returns their paths and a pool
// trusting the certificate.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "haproxy-mcp test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func TestTLSAndBasicAuth(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t)
	cfg := &config.Config{
		TLS:       config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile},
		BasicAuth: config.BasicAuthConfig{Enabled: true, Username: "mcp", Password: "s3cret"},
	}
	opts, err := newHTTPOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, server.NewMCPServer("test", "0"), testRegistry(t), ln, opts, 5*time.Second)
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveHTTP: %v", err)
		}
	}()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 5 * time.Second}
	base := "https://" + ln.Addr().String()
	initialize := func(user, pass string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(initializeBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if user != "" || pass != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}

	if resp := initialize("", ""); resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Errorf("no credentials: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp := initialize("mcp", "wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", resp.StatusCode)
	}
	if resp := initialize("other", "s3cret"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong user: %d", resp.StatusCode)
	}
	if resp := initialize("mcp", "s3cret"); resp.StatusCode != http.StatusOK || resp.Header.Get("Mcp-Session-Id") == "" {
		t.Errorf("right credentials: %d, session %q", resp.StatusCode, resp.Header.Get("Mcp-Session-Id"))
	}

	// The health check needs no credentials (it gives out only counts), over HTTPS.
	resp, err := client.Get(base + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz without credentials: %v %v", resp, err)
	}

	// Plain HTTP on the TLS port gets no MCP answer.
	if resp, err := http.Get("http://" + ln.Addr().String() + "/healthz"); err == nil && resp.StatusCode == http.StatusOK {
		t.Error("plain HTTP must not be served on the TLS port")
	}
}

func TestBasicAuthWithoutTLS(t *testing.T) {
	opts, err := newHTTPOptions(&config.Config{BasicAuth: config.BasicAuthConfig{Enabled: true, Username: "u", Password: "p"}})
	if err != nil || opts.TLS != nil || !opts.BasicAuth.Enabled {
		t.Fatalf("%+v %v", opts, err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, server.NewMCPServer("test", "0"), testRegistry(t), ln, opts, 5*time.Second)
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveHTTP: %v", err)
		}
	}()

	resp, err := http.Post("http://"+ln.Addr().String()+"/mcp", "application/json", strings.NewReader(initializeBody))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /mcp without credentials: %v %v", resp, err)
	}
}

func TestNewHTTPOptionsErrors(t *testing.T) {
	certFile, keyFile, _ := selfSigned(t)
	otherCert, _, _ := selfSigned(t)

	// No TLS, no auth: plain HTTP.
	if opts, err := newHTTPOptions(&config.Config{}); err != nil || opts.TLS != nil || opts.BasicAuth.Enabled {
		t.Errorf("defaults: %+v %v", opts, err)
	}
	// Missing files and a key that does not belong to the certificate are startup errors.
	for name, tc := range map[string]config.TLSConfig{
		"missing cert":   {Enabled: true, CertFile: "/nonexistent/cert.pem", KeyFile: keyFile},
		"missing key":    {Enabled: true, CertFile: certFile, KeyFile: "/nonexistent/key.pem"},
		"mismatched key": {Enabled: true, CertFile: otherCert, KeyFile: keyFile},
	} {
		if _, err := newHTTPOptions(&config.Config{TLS: tc}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// TLS configured but not enabled is ignored.
	if opts, err := newHTTPOptions(&config.Config{TLS: config.TLSConfig{CertFile: "/nonexistent"}}); err != nil || opts.TLS != nil {
		t.Errorf("disabled TLS: %+v %v", opts, err)
	}
}
