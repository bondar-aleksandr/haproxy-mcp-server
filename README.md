# HAProxy MCP Server

[![Build](https://github.com/bondar-aleksandr/haproxy-mcp-server/actions/workflows/build.yml/badge.svg)](https://github.com/bondar-aleksandr/haproxy-mcp-server/actions/workflows/build.yml)
[![Release](https://github.com/bondar-aleksandr/haproxy-mcp-server/actions/workflows/release.yml/badge.svg)](https://github.com/bondar-aleksandr/haproxy-mcp-server/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bondar-aleksandr/haproxy-mcp-server)](https://goreportcard.com/report/github.com/bondar-aleksandr/haproxy-mcp-server)
![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/bondar-aleksandr/haproxy-mcp-server)
![License](https://img.shields.io/github/license/bondar-aleksandr/haproxy-mcp-server)

A Model Context Protocol (MCP) server for HAProxy implemented in Go, leveraging HAProxy Runtime API and mcp-go.

This is a fork of [tuannvm/haproxy-mcp-server](https://github.com/tuannvm/haproxy-mcp-server), reworked into a read-only inspection server for several HAProxy instances (stats page, Runtime API and `haproxy.cfg`). It is not published as a package or image: build it from source as described below.

## Overview

The HAProxy MCP Server provides a standardized way for LLMs to interact with HAProxy's runtime API via the Model Context Protocol (MCP). This enables LLMs to perform HAProxy administration tasks, monitor server status, manage backend servers, and analyze traffic patterns, all through natural language interfaces.

![Screenshot-1](https://github.com/user-attachments/assets/8d3bf7f5-be98-4997-b676-120891692f15)
![Screenshot-2](https://github.com/user-attachments/assets/a443b5d2-8d7d-4daf-a6c1-115912f704d1)
![Screenshot-3](https://github.com/user-attachments/assets/fa387604-4eb9-4456-adc3-5a8395e5ecc1)

## Features

- **Full HAProxy Runtime API Support**: Comprehensive coverage of HAProxy's runtime API commands
- **Context-Aware Operations**: All operations support proper timeout and cancellation handling
- **Stats Page Integration**: Support for HAProxy's web-based statistics page for enhanced metrics and visualization
- **Secure Authentication**: Support for secure connections to HAProxy runtime API
- **Multiple Transport Options**: Supports both stdio and HTTP transports for flexibility in different environments
- **Enterprise Ready**: Designed for production use in enterprise environments
- **Docker Support**: a Dockerfile to build an image

## Documentation

- [docs/tools.md](docs/tools.md): the tools, their arguments and output, and what each reports with and without the stats page, the Runtime API and the configuration file
- [docs/haproxy.md](docs/haproxy.md): setting up HAProxy for the server (Runtime API and its access levels, stats page, the configuration file, security)
- [docs/clients.md](docs/clients.md): the Runtime API commands the server sends
- [docs/design.md](docs/design.md): how it is built and why

## Installation

There are no published releases, packages or images of this fork yet; build it from source (Go 1.25 or later).

### From source

```bash
git clone https://github.com/bondar-aleksandr/haproxy-mcp-server.git
cd haproxy-mcp-server
go build -o bin/haproxy-mcp-server ./cmd     # or: make build
./bin/haproxy-mcp-server --config config.toml
```

`go install github.com/bondar-aleksandr/haproxy-mcp-server/cmd@latest` works as well, but Go names the binary after the last element of the path, so you get one called `cmd`.

### Using Docker

```bash
make docker-build                       # image haproxy-mcp-server:<version> and :latest, with the version information
make docker-run CONFIG=config.toml PORT=8080   # run it with your configuration, published on host port 8080
```

which is the same as:

```bash
docker build -t haproxy-mcp-server \
  --build-arg GIT_VERSION=$(git describe --tags --always --dirty) \
  --build-arg GIT_COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) .
docker run -d --name haproxy-mcp -p 8080:8080 \
  -v $(pwd)/config.toml:/app/config.toml:ro \
  haproxy-mcp-server
```

(`make docker-build` uses the date of the commit as the build date instead of the current time: a date that changes on every call defeats Docker's layer cache and recompiles the program each time.)

The build arguments are optional; they set the version printed by `--version` (`docker run --rm haproxy-mcp-server --version`), shown in the startup log and in the image labels. The image runs as an unprivileged user and starts with `--config /app/config.toml`, so mount your configuration there (or pass another path: `haproxy-mcp-server --config /etc/haproxy-mcp/config.toml`). The server speaks MCP over HTTP on port 8080 unless `[mcp]` in the configuration says otherwise. Settings and credentials can be passed as environment variables, which win over the file, for example `-e HAPROXY_PROD_STATS_URL=http://user:pass@host:8404/stats`. Keep the default port 8080 inside the container and map it to whatever you need outside (`-p 9000:8080`): the image's health check calls `http://127.0.0.1:8080/healthz`.

**Health check.** With the HTTP transport the server answers `GET /healthz` with `200` and `{"status":"ok","instances":{"total":2,"available":1}}` for as long as it is up and serving. It is liveness only and deliberately does not depend on HAProxy: instances are retried in the background and the server is meant to run with none of them available (see `status` in the `haproxy://instances` resource, or the counts above, to monitor those). The counts are all it gives out, no names, URLs or errors. The Dockerfile uses it as its `HEALTHCHECK` (every 30 s, 15 s start period); outside Docker, point a Kubernetes `livenessProbe` or a load balancer at it. The stdio transport has no HTTP port, hence no health check.

The build context excludes `.git`, `*.cfg` and `live_data/` (see `.dockerignore`), so a real `haproxy.cfg` is not copied into the image layers.

## MCP Integration

To use this server with MCP-compatible LLMs, configure the assistant with the following connection details:

Write a `config.toml` describing your HAProxy instances (see [Configuration](#configuration)), then point the server at it:

```json
{
  "mcpServers": {
    "haproxy": {
      "command": "haproxy-mcp-server",
      "args": ["--config", "/etc/haproxy-mcp/config.toml"],
      "env": {
        "MCP_TRANSPORT": "stdio",
        "HAPROXY_PROD_STATS_URL": "http://user:password@10.0.0.1:8404/stats"
      }
    }
  }
}
```

The `env` block is optional; it is the place for sensitive values overriding the file (here the stats URL of the `prod` instance, with credentials). Each instance can use the Runtime API over TCP4 or a Unix socket, the stats page, or both; an instance with only the stats page offers the read-only tools.

> **Note:** For detailed instructions on how to configure HAProxy to expose the Runtime API and Statistics page, see the [HAProxy Configuration Guide](docs/haproxy.md).

## Available MCP Tools

The server provides read-only inspection tools; it deliberately has no tools that change servers, health checks, weights or the configuration, or reload HAProxy.

| Tool | Purpose |
| --- | --- |
| `list_services` | **Start here.** Frontends with their bind addresses and the backends they route to, with backend health |
| `list_frontends` | Frontends: status, binds, routes, counters |
| `list_backends` | Backends and their servers: status, health, counters, frontends using them |
| `show_stat` | Raw statistics table (optional name filter) |
| `show_config` | The raw `haproxy.cfg`, credentials masked (optional section filter); needs `config_file` |
| `show_info`, `debug_counters` | Process information, internal rare-event counters |
| `clear_counters_all` | Reset statistics counters |

What each tool can report depends on what the instance gives the server access to: see [What the server can see](#what-the-server-can-see-stats-runtime-config).

The three `list_*` tools share the same arguments, so the model can go from a first look to a single object without switching tools:

- `detail`: `terse` (default) is the first look (identity, status, health summary, what is wrong); `standard` adds counters, the table of servers and the routes with their conditions; `full` adds every column HAProxy reports (the whole stats row, and for servers the whole `show servers state` row). `list_services` has `terse` and `standard`.
- `name`: only this frontend / backend. For `list_backends`, `server` narrows to one server of that backend.

Objects carry HAProxy's ids (`iid` for frontends and backends, `sid` for servers) to correlate the tools with each other and with `show_stat`.

## Configuration

The server is configured with a TOML file passed via `--config` (required); see [config.example.toml](config.example.toml) for all keys:

```bash
haproxy-mcp-server --config /etc/haproxy-mcp/config.toml
```

Every key can be overridden by an environment variable, which is the recommended way to supply sensitive values such as credentials embedded in a stats URL (`http://user:pass@host:8404/stats`). Precedence: environment variable > config file > built-in default. Credentials in URLs are masked in logs and never appear in the `haproxy://instances` resource.

### HAProxy instances

Define one `[instances.<name>]` table per HAProxy (at least one is required):

```toml
[instances.prod]
description = "Production edge"
[instances.prod.runtime]
mode = "tcp4"
host = "10.0.0.1"
port = 9999
[instances.prod.stats]
url = "http://10.0.0.1:8404/stats"

[instances.staging.stats]          # stats page only: read-only tools
url = "http://10.0.1.1:8404/stats"
```

| Key (under `instances.<name>`) | Environment variable | Description | Default |
| --- | --- | --- | --- |
| `description` | HAPROXY_&lt;NAME&gt;_DESCRIPTION | Free text shown in the instances resource | |
| `address` | HAPROXY_&lt;NAME&gt;_ADDRESS | IP(s) HAProxy is reachable at, a string or an array (env: comma or space separated). Reported as `addresses` in the instances resource instead of the hosts of the Runtime API and stats URLs; set it when clients reach HAProxy at another address than the management one, or when only a unix socket is used | derived from the URLs |
| `config_file` | HAPROXY_&lt;NAME&gt;_CONFIG_FILE | The instance's `haproxy.cfg` (file, or directory of `*.cfg`) as readable from this server; enables bind IPs and frontend-to-backend routes, see below | |
| `runtime.mode` | HAPROXY_&lt;NAME&gt;_RUNTIME_MODE | `tcp4` or `unix`; empty = no Runtime API | |
| `runtime.host` | HAPROXY_&lt;NAME&gt;_HOST | Runtime API host (tcp4 only) | 127.0.0.1 |
| `runtime.port` | HAPROXY_&lt;NAME&gt;_PORT | Runtime API port (tcp4 only) | 9999 |
| `runtime.socket` | HAPROXY_&lt;NAME&gt;_RUNTIME_SOCKET | Runtime API socket path (unix only) | /var/run/haproxy/admin.sock |
| `runtime.url` | HAPROXY_&lt;NAME&gt;_RUNTIME_URL | Direct Runtime API URL, overrides the above | |
| `stats.enabled` | HAPROXY_&lt;NAME&gt;_STATS_ENABLED | Use the stats page | true |
| `stats.url` | HAPROXY_&lt;NAME&gt;_STATS_URL | Stats page URL, e.g. `http://localhost:8404/stats` | |

`<NAME>` is the instance name upper-cased with `-` as `_` (`eu-west` → `HAPROXY_EU_WEST_STATS_URL`). Environment variables only override keys of instances declared in the file; they cannot create instances. Each instance needs the Runtime API, the stats page, or both.

- Every tool takes an `instance` argument (a required enum when several instances are configured, optional with one). A tool that needs the Runtime API fails with a clear error for an instance that only has the stats page.
- The `haproxy://instances` MCP resource lists the configured instances with their description, `status` (`available`/`unavailable`, with `error`, `lastAttempt` and `attempts` when unavailable), `capabilities` (`runtime`, `stats`) and `addresses`: the IPs (hostnames, if the configuration uses names) HAProxy is reachable at: the `address` setting if given, else the hosts of the Runtime API (tcp) and stats page URLs (without ports, paths or credentials). A frontend bound to all addresses (`*:443`) is reachable on these. Instances reached only through a unix socket have no address unless `address` is set. The set of tools depends on the configured APIs, not on which instances are currently reachable.
- An instance that cannot be reached (Runtime API or stats page) does not stop the server: it is listed as `unavailable` in the `haproxy://instances` resource, together with the last error, and tool calls targeting it fail with that error. It is retried in the background every `haproxy.retry_interval` until it connects, then becomes `available`. If every instance is unavailable the server still starts. Reachability is only checked until the first successful connection; an instance is not marked unavailable again later.

### What the server can see: stats, runtime, config

An instance is described by up to three sources. Each tool reports what its sources allow, and says so in `warnings` when something is missing.

| Source | Setting | What it is | What it gives |
| --- | --- | --- | --- |
| **stats** | `stats.url` | HAProxy's stats page, read as JSON over HTTP (basic auth through the URL) | Status and counters of every frontend, backend and server: the `show stat` table |
| **runtime** | `runtime.mode` / `runtime.url` | HAProxy's Runtime API (CLI socket over TCP or unix) | The same `show stat` table, plus the servers' operational state (`show servers state`), the listening ports (`show fd`), process information (`show info`) and the rare-event and statistics counters |
| **config** | `config_file` | A readable copy of the instance's `haproxy.cfg`; not a connection to HAProxy | The static topology: bind addresses, the backends each frontend routes to (`use_backend`, `default_backend`) with their conditions and ACLs |

An instance needs **stats or runtime** (or both); **config** is optional and only adds the static topology. When both stats and runtime are set, the stats page is used for `show stat` data (the runtime is the fallback), and the runtime for everything else.

**Access level of the Runtime API socket.** HAProxy refuses commands above the level of the socket (`stats socket ... level user|operator|admin`). Checked on HAProxy 3.0.9: at `user`, `show info`, `show stat` and `show servers state` work; `show fd` and `debug counters` need `operator`; `clear counters all` is documented by HAProxy as `admin` only. A refused command is reported as an error (`HAProxy refused "show fd": Permission denied`), and a failing `show fd` becomes a warning in the overview tools instead of silently missing binds.

**`show_config` and the raw configuration.** `show_config` returns the instance's `haproxy.cfg` as written, comments included, so that the model can see what the structured tools do not show (redirects, `deny`/`return` rules, timeouts, server options, caching, global settings). An optional `filter` returns only the sections whose header contains it (`frontend web_FE`, `backend app_BE`, `global`, `defaults`). It reads only the configured `config_file` (there is no path argument). Values that look like credentials are masked as `<redacted>` (`stats auth` passwords, `userlist` passwords, `setenv` values, credential-like headers and the values ACLs match on them, `Basic`/`Bearer` tokens, passwords in URLs); this is pattern matching and not a guarantee, so keep secrets out of the file where you can, and check the result before pointing a model you do not control at a sensitive configuration. The result starts with a JSON summary (file, modification time, line ranges of the returned sections, number of masked values, warnings) followed by the text.

**Staleness.** The configuration file is a copy, not what HAProxy has loaded. When the instance has a Runtime API, the server compares the file's modification time with the HAProxy process start time (`show info`) and adds a warning to `show_config` and to the overview tools if the file is newer, i.e. edited but not yet reloaded or restarted.

**What each tool gets, and loses, without a source** (details per tool in [tools.md](docs/tools.md#what-each-tool-needs)):

| Tool | stats only | runtime only | + config |
| --- | --- | --- | --- |
| `list_services` | Frontends and their status; **no binds, no routes** | + listening **ports** (`port 443`); no routes | + **bind addresses and endpoints, routes with conditions, backend health per route** |
| `list_frontends` | Status, mode, counters; no binds, no backends | + listening ports | + bind addresses and endpoints, `backends`, routes with ACLs |
| `list_backends` | Status, servers, health, counters; no `state` | + servers' **operational state** (`RUNNING`, ...), full state rows | + `used_by` (the frontends routing to each backend) |
| `show_stat` | Yes (143 columns) | Yes (raw CSV, ~200 columns, many empty) | no change |
| `show_config` | **not available** without `config_file`; with it, works even when HAProxy is unreachable | same | the raw file (credentials masked) |
| `show_info` | **not available** | Yes | no change |
| `debug_counters`, `clear_counters_all` | **not available** | Yes (operator / admin socket) | no change |

The config file is read from disk (a mounted or copied `haproxy.cfg`), so it describes what that file says, not necessarily what HAProxy is running: check that it is the file the running process was started with (`show env` shows it as `HAPROXY_CFGFILES`).

### Frontends, backends and services

`list_frontends`, `list_backends` and `list_services` describe how traffic flows through an instance:

- `list_frontends`: status, counters, **bind addresses** (`ip:port`, `*` for all addresses, TLS) and **routes** (every `use_backend` rule with its condition and the ACL definitions it uses, then the `default_backend`).
- `list_backends`: status, algorithm, servers (health, address, weight, check result) with up/down counts, counters, and `used_by`, the frontends routing to the backend.
- `list_services`: the joined view, one entry per frontend with its binds and, for each route, the state of the backend (status, servers up/down).

HAProxy does not expose the `bind` IPs or the `default_backend`/`use_backend` rules through the Runtime API or the stats page, so these come from the instance's `haproxy.cfg`, given as `config_file` (mount it into the container if HAProxy runs elsewhere; the path is shown by `show env` as `HAPROXY_CFGFILES`). The file is parsed for `frontend`/`listen`/`defaults`/`backend` sections, `bind`, `acl`, `use_backend`, `default_backend` and `mode`; it is re-read when it changes, and it should be the file the running HAProxy was started with. Without `config_file`, frontends only report their listening ports (from the Runtime API, when available), no routes, and the results carry a `warnings` entry saying so. A `use_backend` with a log-format expression (`%[...]`) is reported as `dynamic`.

**Where clients connect.** Binds are shown as written, followed by the addresses clients actually connect to. A bind to a specific IP (`bind 10.0.0.5:80`) is its own endpoint. A wildcard bind (`*:443`, `0.0.0.0:443`, `[::]:443`) is reachable on the instance's `addresses` (the `address` setting, else the hosts of its Runtime API and stats URLs), so the terse view reads `"*:443 ssl -> 172.18.62.7:443"`, and `standard` has the list in the bind's `endpoints`. An IPv4-only wildcard is not joined with IPv6 addresses; unix sockets have no endpoint. Binds known only from the Runtime API (no `config_file`) carry just the port, as the IP, and whether the bind is a wildcard, are not known.

### Global settings

| Key | Environment variable | Description | Default |
| --- | --- | --- | --- |
| `haproxy.retry_interval` | HAPROXY_RETRY_INTERVAL | How often unavailable instances are retried (Go duration, `0` = never) | 30s |
| `mcp.transport` | MCP_TRANSPORT | MCP transport (`stdio` or `http`) | http |
| `mcp.port` | MCP_PORT | Port for HTTP transport | 8080 |
| `log.level` | LOG_LEVEL | `debug`, `info`, `warn`, `error` | info |

## Security Considerations

- **Authentication**: Connect to HAProxy's Runtime API using secure methods
- **Network Security**: When using TCP4 mode, restrict connectivity to the Runtime API port
- **Unix Socket Permissions**: When using Unix socket mode, ensure proper socket file permissions
- **Input Validation**: All inputs are validated to prevent injection attacks

For comprehensive security best practices and configuration examples, see the [HAProxy Configuration Guide](docs/haproxy.md#security-considerations).

## Development

### Testing

```bash
# Run all tests
go test ./...

# Run tests excluding integration tests
go test -short ./...
```

You can test the HAProxy MCP server locally in several ways:

#### Direct CLI Testing

Build and run the server with a config file:

```bash
go build -o bin/haproxy-mcp-server ./cmd

cp config.example.toml config.toml   # edit the instances
MCP_TRANSPORT=stdio LOG_LEVEL=debug ./bin/haproxy-mcp-server --config config.toml
```

#### Test Individual MCP Tools

With `MCP_TRANSPORT=http` the server answers MCP over HTTP at `/mcp`; any MCP client or inspector can call the tools and read the `haproxy://instances` resource. Each tool call names its target with the `instance` argument, e.g. `{"name":"show_stat","arguments":{"instance":"prod","filter":""}}`.

### Technical Implementation

The HAProxy MCP Server includes several technical improvements designed for reliability and robustness:

- **Context-Aware Operations**: All API calls support context-based timeout and cancellation, allowing graceful termination of long-running operations.
- **Fallback Mechanisms**: Automatic fallback to socat if direct connection fails, ensuring compatibility across different HAProxy deployments.
- **Unified Socket Handling**: Common code for both TCP and Unix socket connections, reducing duplication and improving maintainability.
- **Resilient Connection Management**: Dynamic buffer management for large responses and proper resource cleanup with deadline handling.
- **Comprehensive Error Handling**: Structured error handling and logging for easier troubleshooting.

### Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
