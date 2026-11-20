package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bondar-aleksandr/haproxy-mcp-server/internal/config"
)

// httpOptions secure the HTTP transport. The zero value serves plain HTTP without authentication.
type httpOptions struct {
	TLS       *tls.Config            // nil: plain HTTP
	BasicAuth config.BasicAuthConfig // Enabled: require these credentials on /mcp
}

// newHTTPOptions checks the TLS and basic authentication settings and loads the certificate, so
// that a missing or broken file stops the server at startup, not at the first connection.
func newHTTPOptions(cfg *config.Config) (httpOptions, error) {
	opts := httpOptions{BasicAuth: cfg.BasicAuth}
	if cfg.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return opts, fmt.Errorf("loading the TLS certificate %q and key %q: %w", cfg.TLS.CertFile, cfg.TLS.KeyFile, err)
		}
		opts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	if cfg.BasicAuth.Enabled && !cfg.TLS.Enabled {
		slog.Warn("Basic authentication without TLS: the credentials cross the network in clear text; enable mcp.tls or terminate TLS in front of the server")
	}
	return opts, nil
}

// requireBasicAuth wraps h with HTTP basic authentication against the configured credentials.
func requireBasicAuth(h http.Handler, auth config.BasicAuthConfig) http.Handler {
	// Comparing fixed-size hashes in constant time does not reveal the length or a matching prefix.
	wantUser := sha256.Sum256([]byte(auth.Username))
	wantPass := sha256.Sum256([]byte(auth.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(user))
		gotPass := sha256.Sum256([]byte(pass))
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
		if !ok || !userOK || !passOK {
			if ok {
				slog.Warn("Rejected MCP request: wrong credentials", "remote", r.RemoteAddr)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="haproxy-mcp", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}
