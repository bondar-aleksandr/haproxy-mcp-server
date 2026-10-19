# HAProxy MCP Server Tools

This document describes the MCP tools supported by the HAProxy MCP Server, which allow AI assistants to inspect HAProxy through the Model Context Protocol (MCP). All tools are read-only except `clear_counters_all` (resets the statistics counters); there are no tools that change servers, health checks, weights or the configuration.

Every tool takes an `instance` argument (see the `haproxy://instances` resource) and works on the Runtime API, the stats page, or both, as noted per tool.

## Correlating the tools

Objects carry HAProxy's own ids so the output of the tools can be matched with each other and with `show_stat`: `iid` is the proxy id of a frontend or backend (`iid` in `show stat`, `be_id` in `show servers state`), `sid` the id of a server within its backend (`sid` in `show stat`, `srv_id` in `show servers state`), and `backend_iid` the `iid` of a route's backend in `list_services`.

## What each tool needs

An instance has up to three sources (see the [README](../README.md#what-the-server-can-see-stats-runtime-config)): **stats** (the stats page), **runtime** (the Runtime API socket) and **config** (a readable copy of `haproxy.cfg`, `config_file`). An instance needs stats or runtime; config is optional. The tables below show, per tool, what is reported with each source. "—" means the field or tool is not available; a missing piece is also named in the tool's `warnings`.

### list_services

| Reported | stats | runtime | config |
| --- | --- | --- | --- |
| Frontends, `iid`, status | yes | yes | |
| Bind addresses | — | listening **ports** only (`port 443`; needs an operator-level socket, otherwise a warning) | **yes**: `10.0.0.5:80`, `*:443 ssl -> 172.18.62.7:443` |
| Routes (backend, condition with its ACL definitions, i.e. which hosts go where, default) | — | — | **yes** |
| Health of each route's backend (status, servers up/total) | — | — | **yes** (it hangs on the routes; see `list_backends` for the health of all backends without config) |

### list_frontends

| Reported | stats | runtime | config |
| --- | --- | --- | --- |
| `iid`, name, status, mode | yes | yes | |
| Counters (`standard`): sessions, rates, bytes, errors, 4xx/5xx | yes | yes | |
| Whole stats row (`full`) | yes (143 columns) | yes (142 non-empty columns) | |
| Bind addresses | — | ports only | **yes** (IPs, TLS, endpoints) |
| `backends` and routes with conditions and ACLs (`standard`) | — | — | **yes** |

### list_backends

| Reported | stats | runtime | config |
| --- | --- | --- | --- |
| `iid`, name, status, servers total/up/down/maint, servers that are not UP (`terse`) | yes | yes | |
| Algorithm, counters, table of servers: `sid`, status, address, weight, check result, sessions (`standard`) | yes | yes | |
| Servers' operational `state` (`RUNNING`, `STOPPED`, ...) and `admin_state` | — | **yes** (works with a `user`-level socket) | |
| Whole stats rows of the backend and servers (`full`) | yes | yes | |
| Whole `show servers state` row of each server (`state_details`, `full`) | — | **yes** | |
| `used_by`: the frontends routing to the backend | — | — | **yes** |

### show_stat

| | stats | runtime | config |
| --- | --- | --- | --- |
| The statistics table, optional `filter` (substring of a proxy or server name) | yes: 143 columns, from the stats page | yes: the raw CSV of `show stat`, about 200 columns, many empty | |

With both sources the stats page is used and the runtime is the fallback if the page fails.

### show_config

| | stats | runtime | config |
| --- | --- | --- | --- |
| The raw `haproxy.cfg`, optional `filter` on the section header | — | — | **required**: reads only `config_file`; works even if HAProxy is unreachable |
| Warning that the file is newer than the running process | — | **yes** (compares with the start time from `show info`) | |

Not registered while no instance has a `config_file`; fails for instances without one.

### show_info, debug_counters, clear_counters_all

| Tool | stats | runtime | config |
| --- | --- | --- | --- |
| `show_info` (`show info`: version, uptime, limits, connections) | **not available** | yes, any access level | |
| `debug_counters` (`debug counters`: rare-event counters that are not zero; an empty list is the healthy case) | **not available** | yes, needs an `operator` socket | |
| `clear_counters_all` (`clear counters all`) | **not available** | yes, HAProxy documents it as `admin` only | |

These three are not registered at all while no instance has a Runtime API, and fail with a clear error for instances without one. When the socket's access level is too low, HAProxy's refusal is returned as the tool error.

### Access level of the Runtime API socket

Measured on HAProxy 3.0.9 (`stats socket ... level user|operator|admin`; how to set it: [haproxy.md](haproxy.md#access-level)):

| Command | used by | `user` | `operator` | `admin` |
| --- | --- | --- | --- | --- |
| `show info`, `show stat`, `show servers state` | `show_info`, `show_stat`, `list_*` | yes | yes | yes |
| `show fd` | listening ports in `list_services`, `list_frontends` (only without `config_file`) | no | yes | yes |
| `debug counters` | `debug_counters` | no | yes | yes |
| `clear counters all` | `clear_counters_all` | no | no (not tested) | yes (HAProxy documentation, not tested) |

## Overview tools: `list_services`, `list_frontends`, `list_backends`

Start with `list_services`, then narrow down. The three tools share two arguments:

| Argument | Meaning |
| --- | --- |
| `detail` | `terse` (default), `standard` or `full` (`list_services`: `terse` or `standard`) |
| `name` | Only this frontend (`list_services`, `list_frontends`) or backend (`list_backends`) |
| `server` | `list_backends` only: only this server of the backend in `name`; implies at least `standard` |

What each level contains:

| | `terse` | `standard` | `full` |
| --- | --- | --- | --- |
| `list_services` | Per frontend: `iid`, name, status, binds (`"10.0.0.5:80"`, `"*:443 ssl -> 172.18.62.7:443"`), routes (backend, `backend_iid`, condition with the definitions of its ACLs, default, backend status, servers up/total) | Binds in full (`ip`, `port`, `ssl`, `endpoints`, ...), server counts as an object | as `standard` |
| `list_frontends` | `iid`, name, status, mode, binds, backends | Counters (sessions, rates, bytes, errors, HTTP 4xx/5xx), routes with conditions and ACLs | The whole stats row |
| `list_backends` | `iid`, name, status, servers total/up/down/maint, `used_by`, and the servers that are not UP | Counters, algorithm, table of servers (`sid`, status, address, weight, check result, operational state, sessions) | The whole stats row of the backend and of each server, and each server's whole servers-state row |

Notes:

- **Sources:** `show stat` (or the stats page) for status and counters, `show servers state` (Runtime API) for the operational state of servers, `show fd` for listening ports, and the instance's `haproxy.cfg` (`config_file`) for bind IPs, routes and `used_by`. Without `config_file`, frontends report only their ports and carry a `warnings` entry; without the Runtime API there is no servers state.
- **Binds:** a specific-IP bind is its own endpoint; a wildcard bind is followed by `->` and the instance's addresses with the port (`endpoints` in `standard`). Without a known instance address, or with a socket, nothing is added. Binds known only from the Runtime API show `port 443`.
- **`full`** keeps every non-empty column; empty values (HAProxy's "not applicable") are left out. A whole backend table at `full` is large: use `name` first.
- An unknown `name` or `server` fails with the list of valid values.

## Other tools

Inputs and outputs; what each needs is in [What each tool needs](#what-each-tool-needs).

### show_stat
The raw statistics table.
- **Source**: the stats page, or `show stat`
- **Input**: Optional `filter`, a substring of a proxy or server name (applied by the server)
- **Output**: One row per frontend, backend and server with every column HAProxy reports

### show_config
The raw configuration file of the instance, as written (comments kept), with credential-looking values masked as `<redacted>`.
- **Source**: the instance's `config_file` (a file, or a directory of `*.cfg` files joined in name order, each preceded by a `# ==> name <==` line)
- **Input**: Optional `filter`: only the sections whose header contains it (`frontend web_FE`, `backend app_BE`, `global`, `defaults`; the `defaults` settings apply to the sections after them), separated by a blank line. A filter that matches nothing fails and lists the section headers.
- **Output**: Two text items. First a JSON summary: `instance`, `config_file`, `modified`, `lines` (of the whole configuration), `sections` (header, `first_line`, `last_line` of each returned section; only with a filter), `redacted_values`, `warnings` (for instance that the file is newer than the running process). Second the configuration text.
- **Masking**: best effort, see the README; a secret written in a way not covered is not masked.

### show_info
Version, uptime, limits and connection figures of the HAProxy process.
- **Source**: `show info`
- **Input**: None
- **Output**: The fields of `show info` as key/value pairs

### debug_counters
The rare-event counters HAProxy keeps (failed assertions, glitches, ...).
- **Source**: `debug counters`
- **Input**: None
- **Output**: A list of `{count, type, location}`; empty when no counter is above zero

### clear_counters_all
Resets all statistics counters (the only tool that changes anything).
- **Source**: `clear counters all`
- **Input**: None
- **Output**: Confirmation, or the error HAProxy returned
