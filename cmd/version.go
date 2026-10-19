package main

import (
	"fmt"
	"io"
	"runtime"
)

// Set at build time:
//
//	go build -ldflags "-X main.GitVersion=v1.2.3 -X main.GitCommit=abc1234 -X main.BuildDate=2026-01-02T03:04:05Z" ./cmd
//
// The variables must stay in package main and keep these names. Build the package (./cmd), not main.go
// alone, or version.go is left out and the build fails with "undefined: GitVersion".
var (
	GitVersion = "dev"
	GitCommit  = "none"
	BuildDate  = "unknown"
	GoVersion  = runtime.Version()
	Platform   = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
)

// printVersion writes the build information.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "Version: %s\nCommit: %s\nBuild Date: %s\nGoVersion: %s\nPlatform: %s\n", GitVersion, GitCommit, BuildDate, GoVersion, Platform)
}
