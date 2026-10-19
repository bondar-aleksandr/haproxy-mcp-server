# HAProxy MCP Server

[![Build](https://github.com/tuannvm/haproxy-mcp-server/actions/workflows/build.yml/badge.svg)](https://github.com/tuannvm/haproxy-mcp-server/actions/workflows/build.yml)
[![Release](https://github.com/tuannvm/haproxy-mcp-server/actions/workflows/release.yml/badge.svg)](https://github.com/tuannvm/haproxy-mcp-server/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/tuannvm/haproxy-mcp-server)](https://goreportcard.com/report/github.com/tuannvm/haproxy-mcp-server)
![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/tuannvm/haproxy-mcp-server)
![License](https://img.shields.io/github/license/tuannvm/haproxy-mcp-server)
![Docker Pulls](https://img.shields.io/docker/pulls/tuannvm/haproxy-mcp-server)

A Model Context Protocol (MCP) server for HAProxy implemented in Go, leveraging HAProxy Runtime API and mcp-go.

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
- **Docker Support**: Pre-built Docker images for easy deployment

## Installation

### Homebrew

```bash
# Add the tap
brew tap tuannvm/tap

# Install the package
brew install haproxy-mcp-server
```

### From Binary

Download the latest binary for your platform from the [releases page](https://github.com/tuannvm/haproxy-mcp-server/releases).

### Using Go

```bash
go install github.com/tuannvm/haproxy-mcp-server/cmd/server@latest
```

### Using Docker

```bash
docker pull ghcr.io/tuannvm/haproxy-mcp-server:latest
docker run -it --rm \
  -v $(pwd)/config.toml:/etc/haproxy-mcp/config.toml:ro \
  ghcr.io/tuannvm/haproxy-mcp-server:latest --config /etc/haproxy-mcp/config.toml
```

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

> **Note:** For detailed instructions on how to configure HAProxy to expose the Runtime API and Statistics page, see the [HAProxy Configuration Guide](haproxy.md).

## Available MCP Tools

The server provides read-only inspection tools; it deliberately has no tools that change servers, health checks, weights or the configuration, or reload HAProxy.

| Tool | Purpose | Needs Runtime API |
| --- | --- | --- |
| `list_services` | **Start here.** Frontends with their bind addresses and the backends they route to, with backend health | no |
| `list_frontends` | Frontends: status, binds, routes, counters | no |
| `list_backends` | Backends and their servers: status, health, counters, frontends using them | no (server state: yes) |
| `show_stat` | Raw statistics table (optional name filter) | no |
| `show_info`, `debug_counters` | Process information, internal counters | yes |
| `clear_counters_all` | Reset statistics counters | yes |

The three `list_*` tools share the same arguments, so the model can go from a first look to a single object without switching tools:

- `detail`: `terse` (default) is the first look (identity, status, health summary, what is wrong); `standard` adds counters, the table of servers and the routes with their conditions; `full` adds every column HAProxy reports (the whole stats row, and for servers the whole `show servers state` row). `list_services` has `terse` and `standard`.
- `name`: only this frontend / backend. For `list_backends`, `server` narrows to one server of that backend.

Objects carry HAProxy's ids (`iid` for frontends and backends, `sid` for servers) to correlate the tools with each other and with `show_stat`.

**Stats-only instances:** when an instance has no Runtime API (no `runtime.mode` of `tcp4`/`unix`, no `runtime.url`), only the tools marked "no" above work for it. The tools that need the Runtime API are not registered at all if no instance has it, and fail with a clear error for instances without it.

For a complete list of all supported tools with their inputs, outputs, and corresponding HAProxy Runtime API commands, see the [tools.md](tools.md) documentation.

## Configuration

The server is configured with a TOML file passed via `--config` (required); see [config.example.toml](config.example.toml) for all keys:

```bash
haproxy-mcp-server --config /etc/haproxy-mcp/config.toml
```

Every key can be overridden by an environment variable, which is the recommended way to supply sensitive values such as credentials embedded in a stats URL (`http://user:pass@host:8404/stats`). Precedence: environment variable > config file > built-in default. Credentials in URLs are masked in logs and in the `haproxy://instances` resource.

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
- The `haproxy://instances` MCP resource lists the configured instances with their description, `status` (`available`/`unavailable`, with `error`, `lastAttempt` and `attempts` when unavailable), capabilities (`runtime`, `stats`) and endpoints. The set of tools depends on the configured APIs, not on which instances are currently reachable.
- An instance that cannot be reached (Runtime API or stats page) does not stop the server: it is listed as `unavailable` in the `haproxy://instances` resource, together with the last error, and tool calls targeting it fail with that error. It is retried in the background every `haproxy.retry_interval` until it connects, then becomes `available`. If every instance is unavailable the server still starts. Reachability is only checked until the first successful connection; an instance is not marked unavailable again later.

### Frontends, backends and services

`list_frontends`, `list_backends` and `list_services` describe how traffic flows through an instance:

- `list_frontends`: status, counters, **bind addresses** (`ip:port`, `*` for all addresses, TLS) and **routes** (every `use_backend` rule with its condition and the ACL definitions it uses, then the `default_backend`).
- `list_backends`: status, algorithm, servers (health, address, weight, check result) with up/down counts, counters, and `used_by`, the frontends routing to the backend.
- `list_services`: the joined view, one entry per frontend with its binds and, for each route, the state of the backend (status, servers up/down).

HAProxy does not expose the `bind` IPs or the `default_backend`/`use_backend` rules through the Runtime API or the stats page, so these come from the instance's `haproxy.cfg`, given as `config_file` (mount it into the container if HAProxy runs elsewhere; the path is shown by `show env` as `HAPROXY_CFGFILES`). The file is parsed for `frontend`/`listen`/`defaults`/`backend` sections, `bind`, `acl`, `use_backend`, `default_backend` and `mode`; it is re-read when it changes, and it should be the file the running HAProxy was started with. Without `config_file`, frontends only report their listening ports (from the Runtime API, when available), no routes, and the results carry a `warnings` entry saying so. A `use_backend` with a log-format expression (`%[...]`) is reported as `dynamic`.

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

For comprehensive security best practices and configuration examples, see the [HAProxy Configuration Guide](haproxy.md#security-considerations).

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
