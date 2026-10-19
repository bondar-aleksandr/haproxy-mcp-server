# Design

How the HAProxy MCP server is built and why. For using it see the [README](../README.md), for the tools [tools.md](tools.md), for setting up HAProxy [haproxy.md](haproxy.md).

## Goals

- Let an LLM **inspect** one or several HAProxy instances through MCP: what is served, on which addresses, by which backends, and whether it is healthy.
- Be **read-only**. There are no tools that change servers, weights, health checks or the configuration. The single exception, `clear_counters_all`, resets statistics counters.
- Work with what an environment gives: the stats page, the Runtime API, a copy of `haproxy.cfg`, in any combination, and say what is missing instead of failing.
- Keep what the model sees small and ordered: a terse first look, more on request.
- Never hand credentials to the model.

## Sources

Per instance, up to three (details in the README, "What the server can see"):

| Source | Gives | Used through |
| --- | --- | --- |
| stats page | status and counters of frontends, backends, servers | `internal/haproxy/stats`: HTTP, `<url>;json` |
| Runtime API | the same table, servers' operational state, listening ports, process info, counters | `internal/haproxy/runtime`: TCP or unix socket, one command per connection, `socat` fallback |
| `haproxy.cfg` | binds, frontend-to-backend routes, ACLs | `internal/haproxy/hacfg`: a small reader of the directives that matter, not a validator |

An instance needs the stats page or the Runtime API; the configuration file is optional. Fields that need a missing source are left out and named in the result's `warnings`.

## Components

```
cmd/                    main.go: flags, logging, wiring, the HTTP server (/mcp, /healthz); version.go
internal/config/        configuration: TOML file + environment overrides, per-instance settings
internal/haproxy/
  registry.go           Instance (state, connect, retry) and Registry (the set of instances)
  client.go             HAProxyClient: the stats and runtime clients of one instance
  frontends.go          topology: joins stats, listening ports and configuration
  overview.go           list_services / list_frontends / list_backends at terse, standard, full
  config_raw.go         show_config
  hacfg/                configuration reader, section filter, credential masking
  runtime/, stats/      the two HAProxy clients
  testing/              mocks
internal/mcp/           tool and resource registration
```

## Instances and availability

- Instances are declared in the configuration (`[instances.<name>]`) and kept in a `Registry` for the life of the process. An instance is `available` once a connection to it has succeeded and `unavailable` before that.
- The server **starts even if no instance is reachable**. Unavailable instances are retried in the background (`haproxy.retry_interval`, default 30 s); the `haproxy://instances` resource shows each one's status, last error and addresses. Reachability is only checked until the first successful connection.
- Which tools exist depends on what is *configured* (for example `show_info` is registered if any instance has a Runtime API), not on what is reachable right now. A tool call on an unavailable instance fails with that instance's last error.
- Every tool takes an `instance` argument (an enum of the configured names, required when there are several).

## What the model sees

- **Progressive detail.** `list_services` is the first call; `list_frontends` and `list_backends` take `name`, `server` and `detail` (`terse`, `standard`, `full`) so the model goes from an overview to one object without switching tools. Terse output always carries the identifiers needed to ask for more, and what is wrong (servers that are not UP, missing configuration).
- **Correlation.** HAProxy's own ids (`iid`, `sid`) appear in every view so that results can be matched with each other and with `show_stat`.
- **Raw escape hatch.** `show_stat` and `show_config` return the data as HAProxy has it, for what the structured tools do not model.
- **Honest gaps.** A missing source never produces a guess: a bind without a known IP shows `port 443`, a route that needs the configuration is absent and `warnings` says why. A configuration file newer than the process start time produces a warning, since it may not be what is running.
- **Secrets.** URLs with credentials are masked in logs and in the resource, error texts are scrubbed of passwords, and `show_config` masks credential-looking values (best effort, see the README). The MCP endpoint itself has no authentication: keep it on an internal network.

## Operations

- **Configuration**: a TOML file (`--config`), every key overridable by an environment variable; instances can only be declared in the file.
- **Logging**: `log/slog`, JSON on HTTP, text on stdio. Connection attempts to an unavailable instance are quiet after the first; a retry logs a warning only when the failure reason changes.
- **Shutdown**: on SIGINT/SIGTERM the MCP sessions are closed first (clients keep a standing event stream open, which would otherwise hold the HTTP connections for the whole grace period), then the HTTP server stops, within 5 s.
- **Health**: `GET /healthz` is liveness only and independent of HAProxy; see the README.
- **Build**: `make build`, `make test`, `make docker-build`; the version comes from linker flags (`--version`). The Dockerfile builds a static binary into an Alpine image with `socat`, running as an unprivileged user.

## Testing

Unit tests run against fakes of HAProxy: an `httptest` server for the stats page, a small TCP server speaking the Runtime API (including segmented responses and refused commands), temporary configuration files, and mock clients for the MCP layer. Behaviour that depends on a real HAProxy (access levels, `show fd`, `show servers state` formats) was checked by hand against HAProxy 3.0.9 and is recorded in [haproxy.md](haproxy.md) and [clients.md](clients.md).

## Known limits

- The configuration reader understands `frontend`, `listen`, `defaults`, `backend`, `bind`, `acl`, `use_backend`, `default_backend` and `mode`. Routing done in other ways (`http-request` rules, `use-server`, maps) is not interpreted, and `use_backend` with a log-format expression is reported as dynamic.
- An instance is not marked unavailable again after it has connected once.
- Tested against HAProxy 3.0; the output formats of other versions may differ.
- No published releases, packages or images of this fork yet.
