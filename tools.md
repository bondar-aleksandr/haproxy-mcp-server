# HAProxy MCP Server Tools

This document describes the MCP tools supported by the HAProxy MCP Server, which allow AI assistants to inspect HAProxy through the Model Context Protocol (MCP). All tools are read-only except `clear_counters_all` (resets the statistics counters); there are no tools that change servers, health checks, weights or the configuration.

Every tool takes an `instance` argument (see the `haproxy://instances` resource) and works on the Runtime API, the stats page, or both, as noted per tool.

## Correlating the tools

Objects carry HAProxy's own ids so the output of the tools can be matched with each other and with `show_stat`: `iid` is the proxy id of a frontend or backend (`iid` in `show stat`, `be_id` in `show servers state`), `sid` the id of a server within its backend (`sid` in `show stat`, `srv_id` in `show servers state`), and `backend_iid` the `iid` of a route's backend in `list_services`.

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
| `list_services` | Per frontend: `iid`, name, status, binds (`"*:443 ssl"`), routes (backend, `backend_iid`, condition, default, backend status, servers up/total) | Binds in full, ACL definitions of the conditions, server counts | as `standard` |
| `list_frontends` | `iid`, name, status, mode, binds, backends | Counters (sessions, rates, bytes, errors, HTTP 4xx/5xx), routes with conditions and ACLs | The whole stats row |
| `list_backends` | `iid`, name, status, servers total/up/down/maint, `used_by`, and the servers that are not UP | Counters, algorithm, table of servers (`sid`, status, address, weight, check result, operational state, sessions) | The whole stats row of the backend and of each server, and each server's whole servers-state row |

Notes:

- **Sources:** `show stat` (or the stats page) for status and counters, `show servers state` (Runtime API) for the operational state of servers, `show fd` for listening ports, and the instance's `haproxy.cfg` (`config_file`) for bind IPs, routes and `used_by`. Without `config_file`, frontends report only their ports and carry a `warnings` entry; without the Runtime API there is no servers state.
- **`full`** keeps every non-empty column; empty values (HAProxy's "not applicable") are left out. A whole backend table at `full` is large: use `name` first.
- An unknown `name` or `server` fails with the list of valid values.

## Statistics & Process Info

### show_stat
Retrieves the full statistics table for HAProxy.
- **Runtime API**: `show stat`
- **Input**: Optional filter (substring of proxy or server names, applied by the server)
- **Output**: Full stats table including bytes, sessions, errors

### show_info
Displays HAProxy version, uptime, and process information.
- **Runtime API**: `show info`
- **Input**: None
- **Output**: Version, uptime, process limits, mode

### debug_counters
Shows internal HAProxy counters.
- **Runtime API**: `debug counters`
- **Input**: None
- **Output**: Internal counters (allocations, events)

### clear_counters_all
Resets all statistics counters.
- **Runtime API**: `clear counters all`
- **Input**: None
- **Output**: Confirmation that all stats have been reset
