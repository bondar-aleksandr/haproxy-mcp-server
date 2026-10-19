# HAProxy Runtime API commands used

The commands the MCP server sends to HAProxy's Runtime API, and nothing else. They are all read-only except `clear counters all`. Setting up the socket and its access level is in [haproxy.md](haproxy.md); what each tool reports with and without them is in [tools.md](tools.md).

| Command | Used by | Minimum socket level | Without it |
| --- | --- | --- | --- |
| `show info` | `show_info`; the connection check at startup; the process start time for the "config file is newer than the running process" warning | `user` | no process information, no drift warning |
| `show stat` | `show_stat` and every `list_*` tool, when the stats page is not configured (or fails); the stats page gives the same table | `user` | the stats page is needed instead |
| `show servers state` | the operational state of servers in `list_backends` (`standard`, `full`) | `user` | no `state`, `admin_state` or `state_details` |
| `show fd` | the listening ports of frontends in `list_services` and `list_frontends`, only when there is no `config_file` | `operator` | frontends show no binds, and a warning says why |
| `debug counters` | `debug_counters` | `operator` | the tool fails with HAProxy's refusal |
| `clear counters all` | `clear_counters_all` | `admin` (HAProxy's documentation; not tested) | the tool fails with HAProxy's refusal |

The levels were measured on HAProxy 3.0.9, except where noted.

## How the commands are sent

Each command goes over its own connection (a TCP or unix socket, `runtime.mode`), is answered, and the connection is closed. The response is read until HAProxy closes the connection. If connecting directly fails, the server retries through the `socat` binary, which the Docker image includes. A response that is a refusal (`Permission denied`, `Unknown command`) is turned into an error that mentions the socket's access level.

## What is not used

The Runtime API can do much more (adding and removing servers, weights, maintenance mode, health and agent checks, maps and ACLs, sessions, certificates, `set maxconn`, `enable`/`disable frontend`, ...). The server deliberately sends none of it: it is an inspection tool, and a model that can reconfigure a load balancer is a different risk. Names that appeared in earlier versions of this document and do not exist in current HAProxy (for example `show frontend`, which HAProxy 3.0 does not have) are not used either: frontends come from `show stat`, `show fd` and the configuration file.
