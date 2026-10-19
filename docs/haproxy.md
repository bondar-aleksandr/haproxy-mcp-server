# HAProxy Configuration Guide

How to set up HAProxy so that the MCP server can read it. The server needs **at least one** of the stats page and the Runtime API per HAProxy instance, and can use a copy of `haproxy.cfg` on top of them; what each combination gives is in [tools.md](tools.md#what-each-tool-needs). Everything the server does with HAProxy is read-only, except `clear_counters_all`, which resets the statistics counters.

The examples below were checked with `haproxy -c` on HAProxy 3.0.

## Table of Contents
- [Runtime API](#runtime-api)
  - [TCP socket](#tcp-socket)
  - [Unix socket](#unix-socket)
  - [Access level](#access-level)
- [Stats page](#stats-page)
- [The configuration file](#the-configuration-file)
- [Security considerations](#security-considerations)
- [Combined configuration example](#combined-configuration-example)
- [Troubleshooting](#troubleshooting)

## Runtime API

The Runtime API is HAProxy's command socket. The server uses it for the servers' operational state (`show servers state`), the listening ports (`show fd`), process information (`show info`), the rare-event counters (`debug counters`) and the statistics counters. It also reads the `show stat` table from it when no stats page is configured. The commands are listed in [clients.md](clients.md).

The server connects directly, and falls back to `socat` if the direct connection fails (the Docker image includes it).

### TCP socket

```
global
    stats socket ipv4@127.0.0.1:9999 level operator
    stats timeout 30s
```

In the server's configuration: `runtime.mode = "tcp4"`, `runtime.host`, `runtime.port`.

A TCP socket has **no authentication**: whoever reaches the port can run the commands the level allows. Bind it to a loopback or internal address only (`0.0.0.0` exposes it on every interface), and restrict it with a firewall. When HAProxy and the MCP server run on different hosts, the TCP socket is the usual choice; keep the network between them private.

### Unix socket

```
global
    stats socket /var/run/haproxy/admin.sock mode 660 level operator
    stats timeout 30s
```

In the server's configuration: `runtime.mode = "unix"`, `runtime.socket = "/var/run/haproxy/admin.sock"`. Access is controlled by the file permissions: the user running the MCP server must be able to read and write the socket, for example through the socket's group (`group`/`mode` on the `stats socket` line) or a shared volume when both run in containers. Make sure the directory exists:

```bash
mkdir -p /var/run/haproxy
chown haproxy:haproxy /var/run/haproxy
chmod 755 /var/run/haproxy
```

(`user`, `group` and `mode` on a `stats socket` line set the owner and permissions of the socket *file*; they are not credentials.)

### Access level

A socket refuses the commands above its `level`. The server reports the refusal as an error (`HAProxy refused "show fd": Permission denied`). Measured on HAProxy 3.0.9:

| Level | What works |
| --- | --- |
| `user` | `show info`, `show stat`, `show servers state`: process information, status and counters, servers' operational state. Not `show fd` (the listening ports come back as a warning) and not `debug counters` |
| `operator` | Everything above, plus `show fd` and `debug counters`. **Enough for every tool except `clear_counters_all`** |
| `admin` | Everything, including `clear counters all` |

Use `level operator`. Use `admin` only if you want `clear_counters_all` to work, and prefer a separate, more restricted socket for it. `level user` works too if you accept the warnings. `show cli sockets` on a running HAProxy lists its sockets and levels.

## Stats page

The server reads the stats page as JSON, by appending `;json` to the page's URL, so the URL in its configuration is the page itself:

```
frontend stats
    bind 127.0.0.1:8404
    stats enable
    stats uri /stats
    stats refresh 10s
    stats auth admin:YourSecurePassword
    stats hide-version
```

In the server's configuration: `stats.url = "http://127.0.0.1:8404/stats"`, with the credentials in the URL (`http://admin:YourSecurePassword@host:8404/stats`), best given through the environment (`HAPROXY_<NAME>_STATS_URL`) so that the password stays out of the file. `stats uri` must match the path of the URL.

Do **not** add `stats admin`: it lets whoever can open the page disable and drain servers from it, and the MCP server only reads.

Check it:

```bash
curl -s -u admin:YourSecurePassword 'http://127.0.0.1:8404/stats;json' | head -c 300
```

## The configuration file

Binds and frontend-to-backend routes are not exposed by the Runtime API or the stats page, so the server reads them from a copy of `haproxy.cfg` (`config_file`), and `show_config` returns it. It must be readable by the MCP server: mount it into the container, or copy it. It can be a single file or a directory of `*.cfg` files (read in name order, like `haproxy -f <dir>`).

- Use the file the running HAProxy was started with. `show env` on the Runtime API prints it as `HAPROXY_CFGFILES`. The server compares the file's modification time with the process start time and warns when the file is newer, i.e. edited but not yet reloaded.
- The file is returned to the model with the values that look like credentials masked (`stats auth` passwords, `userlist` passwords, `setenv` values, credential-like headers and the values ACLs match on them, `Basic`/`Bearer` tokens, passwords in URLs). That is pattern matching, not a guarantee: keep secrets out of the file where you can (environment variables, files referenced by path).
- Apply configuration changes with a reload (`systemctl reload haproxy`); the server picks up the changed file by itself.

## Security considerations

1. **No authentication on the Runtime API.** A TCP socket is open to whoever reaches it. Prefer a unix socket on the same host, or bind the TCP socket to an internal address and firewall it.
2. **Least privilege.** `level operator` is enough for reading; keep `level admin` off the socket the MCP server uses unless `clear_counters_all` is needed.
3. **Stats page.** Require authentication (`stats auth`), bind it to an internal address, use HTTPS if it crosses an untrusted network (`bind *:8404 ssl crt /path/to/cert.pem`), and do not enable `stats admin`.
4. **Credentials.** Pass the stats URL with its password through the environment, not the configuration file.
5. **The MCP endpoint itself** (`/mcp`) has no authentication either: keep it on an internal network or behind a reverse proxy that authenticates.

## Combined configuration example

```
global
    log /dev/log local0
    stats socket /var/run/haproxy/admin.sock mode 660 level operator
    stats socket ipv4@127.0.0.1:9999 level operator
    stats timeout 30s

defaults
    log     global
    mode    http
    option  httplog
    option  dontlognull
    timeout connect 5000
    timeout client  50000
    timeout server  50000

frontend stats
    bind 127.0.0.1:8404
    stats enable
    stats uri /stats
    stats refresh 10s
    stats auth admin:YourSecurePassword
    stats hide-version

# Your other frontend/backend configurations...
```

And the matching instance in the MCP server's configuration:

```toml
[instances.prod]
config_file = "/etc/haproxy-mcp/prod-haproxy.cfg"   # optional: a copy of haproxy.cfg

[instances.prod.runtime]
mode = "tcp4"
host = "127.0.0.1"
port = 9999

[instances.prod.stats]
url = "http://127.0.0.1:8404/stats"                 # credentials: HAPROXY_PROD_STATS_URL
```

## Troubleshooting

An instance that cannot be reached is listed as `unavailable` in the `haproxy://instances` resource, with the last error, and is retried in the background.

1. **Permission denied** from a tool: the socket's `level` is too low for the command (see [Access level](#access-level)), or, for a unix socket, the file permissions.

2. **Verify the socket is listening:**
   - TCP: `ss -ltn | grep 9999`
   - Stats page: `ss -ltn | grep 8404`

3. **Test the connections directly:**
   ```bash
   # TCP socket
   echo "show info" | socat tcp-connect:127.0.0.1:9999 stdio

   # Unix socket
   echo "show info" | socat unix-connect:/var/run/haproxy/admin.sock stdio

   # Which sockets exist, and at what level
   echo "show cli sockets" | socat tcp-connect:127.0.0.1:9999 stdio

   # Stats page as the server reads it
   curl -s -u admin:YourSecurePassword 'http://127.0.0.1:8404/stats;json' | head -c 300
   ```

4. **Stats page returns 401 / 404:** wrong or missing credentials in `stats.url`, or `stats uri` differs from the URL's path.

5. **A warning that the config file is newer than the running process:** reload HAProxy, or use the file HAProxy actually runs.

6. **Check HAProxy's logs:** `journalctl -u haproxy -f`, or your syslog target.

For more detail see the official [Management Guide](https://docs.haproxy.org/3.0/management.html) (the Runtime API commands and the stats socket) and the [Configuration Manual](https://docs.haproxy.org/3.0/configuration.html).
