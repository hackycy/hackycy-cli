# Tunnel Command

This directory contains `ycy tunnel`: a lightweight control plane around pinned official FRP binaries. ycy owns client enrollment, desired tunnel configuration, synchronization, and FRP process supervision; FRP owns only traffic forwarding.

This README is the single source of truth for tunnel domain language, architecture decisions, implementation contracts, and acceptance criteria.

## Goals

```text
ycy tunnel server
ycy tunnel connect --server tunnel.example.com --token <client-token>
```

- Start one public tunnel control plane and one supervised `frps` child.
- Enroll multiple trusted native clients with one recoverable token per client.
- Let multiple Control Plane Accounts manage owned resources through fixed Administrator and User roles.
- Configure each client's tunnels centrally and push complete versioned snapshots.
- Support HTTP by exact custom domains and path prefixes, and TCP/UDP by public server port.
- Keep the server supervisor and each Client Connection Instance single-instance and foreground while allowing several client instances on one host.
- Work as a native command on the CLI's existing platforms and as a server-oriented Docker image.
- Reuse a pinned, verified official FRP release instead of implementing a forwarding protocol.

Runtime logging is written to the foreground terminal's `stderr`. Set the process-wide level with `ycy --log-level debug|info|warn|error tunnel server` or `ycy --log-level debug|info|warn|error tunnel connect`, or set `YCY_LOG_LEVEL`; the root CLI value takes precedence over the environment and the default is `info`. Tunnel subcommands do not define their own log-level option. Interactive terminals use ANSI colors in the timestamp, level, and scope prefix so levels remain easy to distinguish; redirected and piped output remains plain text. Records include an ISO timestamp, level, scope, and safe structured context. Tokens, passwords, authorization headers, cookies, request bodies, and complete FRP configurations are never logged. Logs are not retained by ycy.

## Non-Goals

- HTTPS tunnel type, wildcard hostnames, URL rewriting, or automatic certificates.
- DNS provider, Nginx Proxy Manager, firewall, or Docker port-management integrations.
- Untrusted tenants, custom roles, configurable permissions, resource transfer, per-client FRP authorization, or FRP server plugins.
- Client containers or a separate client Docker image.
- Endpoint health checks, traffic graphs, retained logs, analytics, or a replacement FRP dashboard.
- Self-daemonization, self-restart, or operating-system service installation.
- Starting a client from cached state before it authenticates with the control plane.

## Ubiquitous Language

**Trusted Tunnel Client**:
A machine inside a private network that is administered by the tunnel operator and applies Tunnel Definitions assigned by the Tunnel Control Plane.
_Avoid_: Untrusted client, tenant

**Client Token**:
The control-plane-generated, sole user-facing identity credential for one Trusted Tunnel Client. It is recoverable and permits at most one active control session.
_Avoid_: Client ID, user, shared token

**Client Connection Instance**:
One local foreground `ycy tunnel connect` supervisor identified by a Tunnel Control Plane origin and Client Token pair. It owns one isolated state directory, one lock, and at most one `frpc` child.
_Avoid_: Trusted Tunnel Client, profile, daemon

**Remembered Tunnel Connection**:
One locally encrypted, previously authenticated Tunnel Control Plane origin and Client Token pair available for interactive connection selection. It is a convenience fallback, not a service-manager identity or an alias.
_Avoid_: Profile, client name, Trusted Tunnel Client

**Client Remark**:
An optional, multi-line, operator-maintained note used to distinguish a Trusted Tunnel Client. It is not an identity credential and need not be unique.
_Avoid_: Client ID, Client Token, username

**Tunnel Control Plane**:
The central authority where Trusted Tunnel Clients are enrolled and their desired Tunnel Definitions are managed.
_Avoid_: FRP dashboard, client configuration file

**Control Plane Account**:
A human operator identity that authenticates to the control UI and owns or administers Trusted Tunnel Clients.
_Avoid_: Trusted Tunnel Client, Client Token, tenant

**Account Role**:
One of two fixed authorization levels. An Administrator can manage every resource, all local Control Plane Accounts, and frps; a User can fully manage only owned resources.
_Avoid_: Permission set, policy, custom role

**Deployment Administrator**:
The stable Administrator identity whose username and password are managed by server environment variables. It cannot be changed or deleted through the control UI.
_Avoid_: Shared account, emergency bypass

**Resource Owner**:
The Control Plane Account that creates a Trusted Tunnel Client. Every Tunnel Definition inherits that client's Resource Owner, including definitions later changed by an Administrator.
_Avoid_: Tunnel creator, mutable assignee

**Tunnel Definition**:
A desired, independently enabled HTTP or port proxy assigned to one Trusted Tunnel Client by the Tunnel Control Plane.
_Avoid_: FRP proxy configuration

**HTTP Tunnel**:
A mapping from one or more normalized exact Custom Domain aliases and one optional Location to a Local Endpoint. Every domain-location pair remains reserved while the Tunnel Definition exists, including when disabled.
_Avoid_: Domain tunnel, HTTPS tunnel

**Location**:
A case-sensitive HTTP path prefix used with each Custom Domain to select a Tunnel Definition. It selects a backend but does not remove or rewrite the request path.
_Avoid_: Rewrite rule, regular expression

**Port Tunnel**:
A TCP or UDP mapping from a public server port to a Local Endpoint. The protocol-specific port remains reserved while the Tunnel Definition exists, including when disabled.
_Avoid_: Domain tunnel, HTTP tunnel

**Server Port Pool**:
The configured range from which public ports for Port Tunnels may be allocated automatically or selected explicitly. A numeric port may be assigned once per transport protocol.
_Avoid_: Local port, unrestricted port

**Local Endpoint**:
The host and port reachable from a Trusted Tunnel Client that receives forwarded traffic.
_Avoid_: Server port, public endpoint

**Desired Revision**:
The latest complete, versioned set of Tunnel Definitions assigned to a Trusted Tunnel Client by the Tunnel Control Plane.
_Avoid_: Incremental update, pending command

**Applied Revision**:
The most recent Desired Revision that a Trusted Tunnel Client successfully validated and activated.
_Avoid_: Online status, desired revision

**Public Ingress**:
The externally managed DNS, TLS, and reverse-proxy path that delivers requests for an HTTP Tunnel hostname to the tunnel server.
_Avoid_: Tunnel Definition, Tunnel Control Plane

## Architecture Decisions

| Decision | Rationale |
| --- | --- |
| Run server and client supervisors in the foreground. | Docker or the host service manager owns supervisor persistence and restart, keeping process ownership, signals, and logs predictable across platforms. |
| Isolate Client Connection Instances by Control Plane origin and Client Token. | A keyed opaque directory identity allows unrelated connections to run in parallel without exposing Client Tokens in paths, while the same pair retains single-instance ownership. |
| Persist control-plane state in one embedded SQLite database. | Transactions, ownership queries, uniqueness, and cascade deletion do not require an external database; connection and process status remain bounded in memory while account sessions are durable files in the server data directory. |
| Keep current Client Tokens recoverable. | Authorized operators can retrieve credentials after creation; control-plane access, its data directory, and backups are therefore trusted, and rotation is the revocation mechanism. |
| Use fixed Administrator and User roles with client-level ownership. | The two roles cover global operation and self-service without configurable policies; Tunnel Definitions inherit their client's owner so snapshots remain complete. |
| Keep one environment-managed Deployment Administrator. | A stable, non-deletable Administrator preserves operational recovery while local account credentials remain database-managed. |
| Apply complete snapshots and retain the last Applied Revision. | A failed Desired Revision must not disrupt unrelated working tunnels, and cached state never authorizes a cold start. |
| Enforce Client Token revocation cooperatively in ycy. | Native FRP is identity-agnostic; trusted clients stop on explicit revoke or rejected authentication without adding FRP plugins or a fork. |
| Use one WebSocket agent control channel. | The channel carries authentication, snapshots, acknowledgements, process state, liveness, and cooperative revocation while FRP remains the data plane. |
| Pin one official FRP version per ycy release. | A generated, verified manifest keeps every native target and the Docker image on one tested distribution without hand-maintained artifact tables. |
| Separate Client Tokens from the Internal FRP Token. | Per-client identity belongs to ycy; one ycy-owned token authenticates its managed FRP data plane and is never shown in the UI. |
| Disable FRP Dashboard, Admin API, metrics, polling, and retained logs. | The operational UI reports only bounded control and process state, avoiding a second management plane and unbounded traffic history. |
| Configure one process-wide logger at the CLI root. | Lifecycle, control-link, configuration, and supervised FRP output share one level filter and terminal format; tests replace the singleton sink without retaining logs. |
| Publish one server-oriented Linux x64/arm64 image. | The image uses the same ycy binary and pinned FRP distribution as native releases; clients remain native and no client-container workflow is maintained. |

## Topology

```text
Control Plane Account browser
  -> ycy control UI/API/WebSocket :7500
       -> SQLite
       -> one supervised frps process

Native ycy tunnel client
  -> control WebSocket :7500 through NPM/TLS when configured
  -> one supervised frpc process
       -> frps data connection :7000

HTTP request
  -> externally managed DNS/TLS/NPM
  -> frps HTTP vhost :8080
  -> frpc
  -> configured localHost:localPort

TCP/UDP request
  -> externally managed DNS/firewall/port publication
  -> frps port pool :20000-20100
  -> frpc
  -> configured localHost:localPort
```

FRP does not see or validate Client Tokens. Client Tokens authenticate a native agent to ycy only. The separate Internal FRP Token is shared with trusted agents after ycy authentication and is used only for native frpc-to-frps authentication. ycy starts the only supported `frps` process and uses this same token in its generated `frps.toml` and every generated `frpc.toml`. The token is generated and persisted by default, or `YCY_TUNNEL_FRP_TOKEN` supplies a fixed value for that ycy-managed `frps`.

## Command Contract

The root `--log-level` option applies to tunnel server and client processes. It is one global CLI option rather than a separate option on either tunnel subcommand; its precedence is the CLI value, `YCY_LOG_LEVEL`, then `info`.

Both commands remain in the foreground until signaled. The server and every Client Connection Instance acquire an exclusive lock for their state directory before opening listeners or starting FRP. A second client using the same normalized origin and Client Token fails with `INSTANCE_ACTIVE`; instances with either value different use independent locks and may run concurrently.

### Server

```text
ycy tunnel server [options]

Options:
      --address <address>                  default 0.0.0.0
      --control-port <port>                default 7500
      --frp-port <port>                    default 7000
      --http-port <port>                   default 8080
      --port-range <start-end>             default 20000-20100
      --advertise-frp-addr <host:port>     default derived from agent request host
      --data-dir <path>                    default platform-specific server directory
      --session-idle-days <days>           default 7
```

Non-secret option precedence is CLI option, environment variable, then default:

| Option | Environment variable |
| --- | --- |
| `--address` | `YCY_TUNNEL_ADDRESS` |
| `--control-port` | `YCY_TUNNEL_CONTROL_PORT` |
| `--frp-port` | `YCY_TUNNEL_FRP_PORT` |
| `--http-port` | `YCY_TUNNEL_HTTP_PORT` |
| `--port-range` | `YCY_TUNNEL_PORT_RANGE` |
| `--advertise-frp-addr` | `YCY_TUNNEL_ADVERTISE_FRP_ADDR` |
| `--data-dir` | `YCY_TUNNEL_DATA_DIR` |
| `--session-idle-days` | `YCY_TUNNEL_SESSION_IDLE_DAYS` |

The Deployment Administrator is a stable account configured through environment variables. Its password has no default and the server refuses to start until a valid password is supplied. Local accounts are then managed through the Administrator UI.

| Setting | Environment variable | Default |
| --- | --- | --- |
| Username | `YCY_TUNNEL_ADMIN_USER` | `admin` |
| Password | `YCY_TUNNEL_ADMIN_PASSWORD` | required |
| FRP authentication token | `YCY_TUNNEL_FRP_TOKEN` | generated and persisted in the server data directory |
| Session idle lifetime | `YCY_TUNNEL_SESSION_IDLE_DAYS` | 7 days |

Account usernames contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens and compare case-insensitively. Passwords contain 5-256 characters. `YCY_TUNNEL_FRP_TOKEN` must be non-empty when set; it optionally fixes the one token used by ycy's own managed `frps` and every generated `frpc`. It does not connect ycy to an externally managed `frps`; that deployment model is unsupported. It is not exposed in the management UI or API. The UI displays deployment settings but cannot mutate them. Changing a listener, port pool, Deployment Administrator credential, FRP authentication token, data directory, or advertised endpoint requires restarting the ycy supervisor through Docker or the host service manager.

### Client

```text
ycy tunnel connect [--server <control-plane>] [--token <client-token>]
```

| Value | CLI option | Environment variable | Secret file | Local fallback |
| --- | --- | --- | --- | --- |
| Server | `--server` | `YCY_TUNNEL_SERVER` | - | Remembered connection selector, then `DEFAULT_TUNNEL_SERVER` |
| Token | `--token` | `YCY_TUNNEL_TOKEN` | `YCY_TUNNEL_TOKEN_FILE` | Token from the selected remembered connection |

Field precedence is CLI option, environment value, Token secret file, Remembered Tunnel Connections, then the compile-time `DEFAULT_TUNNEL_SERVER`, which is empty by default. A server without a URL scheme becomes `https://<server>`; an explicit `http://` URL enables an unencrypted local deployment.

A complete server-token pair starts without prompting. With only a server, saved pairs are filtered by normalized origin. With only a previously saved Token, its matching origins are candidates; a new Token instead uses the distinct saved origins as rotation candidates. With neither value, every valid Remembered Tunnel Connection is a candidate. One candidate is selected automatically. Several candidates use an interactive selector ordered by most recent successful authentication; its labels contain the origin and a Token with only its first eight and last four characters visible. A non-interactive process with several candidates fails and requires both values explicitly. Cancelling the selector creates no state and changes no configuration.

Supplying a CLI token marks the authenticated pair for insertion or recency update. Selecting or otherwise reusing an existing saved pair updates its recency. New Tokens sourced only from the environment or secret file are not persisted. Up to 32 pairs are retained using least-recently-authenticated eviction. Each Client Token is encrypted with the existing machine-and-user-bound ycy configuration key in `~/.ycy-cli/config.json`; one corrupt entry is ignored without hiding the rest. Configuration mutation uses a cross-process lock and atomic replacement so parallel client authentication cannot lose entries. A write failure warns without stopping the authenticated tunnel client.

| Startup combination | Local result |
| --- | --- |
| Same server + same Token | Same instance lock; the second process fails with `INSTANCE_ACTIVE`. |
| Same server + different Token | Independent state directories and `frpc` children. |
| Different server + same Token | Independent state directories and `frpc` children. |
| Different server + different Token | Independent state directories and `frpc` children. |

## Listener Contract

| Listener | Default | Purpose |
| --- | ---: | --- |
| ycy control HTTP | `0.0.0.0:7500/tcp` | Account UI/API, agent WebSocket, liveness. |
| FRP bind | `0.0.0.0:7000/tcp` | frpc data-plane connections. |
| FRP HTTP vhost | `0.0.0.0:8080/tcp` | Host-header routing after external ingress. |
| FRP port pool | `0.0.0.0:20000-20100/tcp+udp` | TCP and UDP tunnels. |

NPM must proxy the control-plane route with WebSocket upgrade support. HTTP tunnel routes must preserve the original `Host` header when proxying to port 8080. ycy does not provision either route.

The advertised FRP endpoint defaults to the hostname used by an authenticated agent plus the configured FRP port. `YCY_TUNNEL_ADVERTISE_FRP_ADDR` overrides it when NAT, port mapping, or separate public hostnames make the listening and advertised addresses different.

## Domain Invariants

### Control Plane Account And Ownership

- A local account has one immutable, case-insensitively unique username, one Argon2id password hash, and exactly the `admin` or `user` Account Role.
- The Deployment Administrator has a stable internal key. Its current username updates from the environment without changing existing Resource Ownership; its password is never stored in SQLite.
- A username collision between the Deployment Administrator and a local account prevents server startup instead of hiding or overwriting either identity.
- Creating a Trusted Tunnel Client assigns the authenticated account as its immutable Resource Owner. Creating or editing a Tunnel Definition never changes that owner.
- A User can list, inspect, edit, delete, rotate, and restart only owned clients and their complete Tunnel Definition sets. A cross-owner resource ID is indistinguishable from a missing ID.
- An Administrator has the same operations across all clients, plus account and frps management. Administrator changes to another account's resources preserve their owner.
- An account that still owns a Trusted Tunnel Client cannot be deleted. The Deployment Administrator cannot be deleted, demoted, or have its password changed through the UI.
- Password changes, password resets, role changes, and account deletion revoke every session for that account immediately. Sessions are opaque 256-bit IDs whose hashes and credential revisions live under `<data-dir>/sessions`; they expire after seven idle days, have no absolute maximum age, survive a restart when the account credentials are unchanged, and remain bounded to eight per account and 128 total.

### Client Token

- The control plane generates a URL-safe random token; authorized accounts cannot choose its contents.
- The token is the only client identity credential and is recoverable from the trusted control plane.
- One token permits at most one active agent WebSocket. A second connection is rejected.
- Rotation atomically replaces the token, asks the connected agent to stop, and retains all tunnel definitions.
- Deletion asks a connected agent to stop, deletes all owned tunnels, and releases their reservations.
- Revocation is cooperative. An unreachable agent can continue an existing FRP session until it reconnects and ycy rejects the old token.

An internal database key may maintain references across token rotation, but it is not part of the UI or public control protocol.

### Client Remark

- A Trusted Tunnel Client may have an optional, multi-line Client Remark of up to 100 characters.
- The remark is trimmed, preserves internal line breaks, need not be unique, and may be edited or cleared without changing the Client Token, Agent session, or Desired Revision.
- An empty remark displays as `Unlabeled client` until an authorized account adds one.

### HTTP Tunnel

```text
customDomains x one optional location -> localHost:localPort
```

- One Tunnel Definition contains 1-32 normalized exact Custom Domains and zero or one Location. A second independently enabled Location is a second Tunnel Definition.
- Custom Domain schemes, paths, ports, IP addresses, `*`, and other wildcard patterns are rejected.
- Internationalized Custom Domains are stored in normalized ASCII form and de-duplicated case-insensitively.
- A Location starts with `/` and is matched as a case-sensitive literal prefix. Query strings do not participate in routing and the matched prefix remains in the request sent to the Local Endpoint.
- An omitted Location is an all-path catch-all. It may coexist with more specific Tunnel Definitions on the same Custom Domain; FRP selects the most specific matching prefix.
- Every `(customDomain, location)` pair is globally unique. Disabled tunnels retain all pair reservations; deletion releases them.
- Several HTTP tunnels may target the same Local Endpoint.

### Port Tunnel

```text
protocol + serverPort -> localHost:localPort
```

- Protocol is exactly `tcp` or `udp`.
- `serverPort` must be inside the configured Server Port Pool.
- The pair `(protocol, serverPort)` is globally unique.
- The same numeric port may be assigned once for TCP and once for UDP.
- Disabled tunnels retain their port reservation; deletion releases it.
- An omitted server port is allocated transactionally from the lowest free port for that protocol.

### Local Endpoint

- `localHost` defaults to `127.0.0.1` and may be an IP address, hostname, or container service name reachable by the native client.
- `localPort` must be an integer from 1 through 65535.
- ycy does not probe Local Endpoints. An unreachable or incorrectly configured endpoint is an operator-maintained condition, not a control-plane health state.

### Revisions

- Every tunnel mutation for a client increments its monotonic Desired Revision in the same SQLite transaction.
- The server always sends the complete current snapshot, never a chain of incremental edits.
- The agent acknowledges the Applied Revision only after the configuration is validated and activated.
- Offline clients may be configured. Their enabled tunnels remain Pending until the client reconnects and applies the latest revision.
- A failed revision remains desired while the last successfully applied revision continues running.

New tunnels default to Enabled but the creation form allows them to be created disabled. Saving, editing, enabling, disabling, or deleting immediately produces and pushes a new Desired Revision.

## Persistent State

The server uses one embedded SQLite database under its data directory. A minimal schema owns:

```text
meta
  schema_version
  internal_frp_token

accounts
  internal_id
  kind (environment | local)
  username
  username_key (unique)
  role (admin | user)
  password_hash (local only)
  created_at
  updated_at

clients
  internal_id
  owner_account_id (foreign key, restrict delete)
  remark
  token (unique, recoverable)
  desired_revision
  last_applied_revision
  revocation_pending
  created_at
  rotated_at

tunnels
  id
  client_internal_id (foreign key, cascade delete)
  label
  protocol (http | tcp | udp)
  custom_domains (HTTP-only JSON array)
  location (HTTP only, nullable catch-all)
  server_port (TCP/UDP only)
  local_host
  local_port
  enabled
  options_json (typed transport, health-check, and HTTP options)
  created_at
  updated_at

tunnel_http_routes
  tunnel_id (foreign key, cascade delete)
  hostname
  location
```

SQLite indexes enforce account username and client owner lookups, normalized HTTP domain-location uniqueness, and `(protocol, server_port)` uniqueness. Type-specific checks prevent an HTTP row from carrying a server port or a TCP/UDP row from carrying Custom Domains. Tunnel mutations, route reservations, and revision increments are atomic. Rotation marks revocation pending until an agent authenticates with the replacement token.

Connection presence, child-process state, reconnect backoff, and the latest structured runtime error stay in bounded process memory. The server does not store metrics, traffic samples, complete logs, or revision history.

The client state root contains one directory per Client Connection Instance:

```text
client/
  v1_<base64url HMAC-SHA-256 digest>/
    last-applied.json
    frpc.toml
```

The digest uses the local configuration encryption key and the domain-separated message `ycy:tunnel-client-instance:v1\0<server.origin>\0<token>`. The complete 32-byte digest is encoded, so the same normalized pair is stable on one machine and user account while paths disclose no Token. A short-lived root registry lock serializes instance creation and cleanup; the per-instance lock remains held for the client process lifetime.

An instance directory may contain a last-applied snapshot and generated FRP configuration for rollback, but neither file authorizes a cold start. Every new ycy client process must authenticate and receive the current Desired Revision before starting frpc. On startup, versioned sibling directories unused for 90 days are removed only when their instance lock is absent or stale. Current, active, recent, unrecognized, and legacy root entries are untouched. The old single connection configuration and old single-directory client state are not read or migrated.

## Agent Control Protocol

The client upgrades `GET /api/agent` to WebSocket with `Authorization: Bearer <client-token>`. JSON messages use an explicit protocol version and tagged `type`; unknown required protocol versions are rejected before FRP starts.

### Handshake

The client reports:

```text
tunnelProtocolVersion
ycyVersion
platform
architecture
lastAppliedRevision
```

The server responds with:

```text
tunnelProtocolVersion
requiredFrpVersion
FRP artifact URL and SHA-256 for this platform
advertised FRP host and port
Internal FRP Token
desired revision and complete tunnel snapshot
```

A client proceeds only when its tunnel protocol implementation and pinned FRP manifest support the advertised combination. Otherwise it leaves frpc stopped, prints an upgrade instruction, and appears as `Incompatible client`.

### Runtime Messages

| Direction | Message | Purpose |
| --- | --- | --- |
| Server -> client | `desired_state` | Replace the pending complete snapshot. |
| Client -> server | `apply_result` | Report applied revision or one structured error. |
| Client -> server | `process_state` | Report `stopped`, `running`, `recovering`, or `configuration_failed`. |
| Server -> client | `restart_frpc` | Restart the sole child from the Applied Revision. |
| Server -> client | `revoke` | Stop frpc and terminate the agent for rotation or deletion. |

WebSocket liveness is the only periodic control-plane check. An ordinary control-link failure leaves an already-running frpc process active and starts capped reconnect backoff. An explicit `revoke` or an authentication rejection stops frpc. A disconnected old-token client can remain `Revocation pending` because FRP deliberately does not enforce ycy identity.

## FRP Binary Management

Each ycy release contains a compile-time manifest for one tested FRP version. The initial implementation target is official FRP `v0.70.1`, subject to updating the pinned version before implementation if the ycy release selects another tested version.

The manifest maps each supported OS/architecture to the official archive name, binary name, URL, and SHA-256. Supported native targets follow the existing ycy matrix: macOS, Linux, and Windows on x64 and arm64.

Only `frp/version.ts` is release-specific hand-maintained input. After changing it, run `bun run generate:frp-manifest`. The generator resolves the fixed target matrix from the tagged official GitHub Release, verifies the GitHub asset digests against `frp_sha256_checksums.txt`, downloads and verifies each archive, derives the extracted binary digests, and writes `manifest.generated.ts` deterministically. `bun run check:frp-manifest` repeats that process without writing and fails when the committed manifest is stale; release CI runs this check before native or Docker builds. The generated file must not be edited manually.

On native startup:

1. Resolve the one fixed managed path for the pinned version and platform.
2. If the binary exists, verify its SHA-256 and reported version.
3. If absent, download the official release archive, verify it before extraction, publish atomically, and verify the executable.
4. If download fails, print the exact version, official URL, expected SHA-256, and fixed target path for manual placement.

The system `PATH` is never consulted and no custom FRP path override exists. The Docker build installs the same pinned official distribution at the image's fixed managed path and includes the required Apache-2.0 attribution.

## Generated FRP Configuration

ycy owns all generated TOML and starts the only supported `frps`. Operators never need to maintain `frps.toml` or `frpc.toml`, and must not run an independent `frps` on the configured FRP bind or HTTP vhost ports.
The generated document is mapped from the typed tunnel model, then passed through
one TOML codec adapter. Its layout is not a compatibility contract; the codec
owns both parsing and serialization so a future TOML implementation can replace
the current one without changing the FRP renderers. A Control Plane Account may
use the Client detail `Import configuration` workflow as a one-time migration
source for supported `[[proxies]]` TOML entries; it is parsed into typed Tunnel
Definitions and is never executed as raw FRP configuration.

The server configuration is equivalent to:

```toml
bindAddr = "0.0.0.0"
bindPort = 7000
vhostHTTPPort = 8080
custom404Page = "<data-dir>/404.html"

auth.method = "token"
auth.token = "<configured-or-generated-frp-token>"

allowPorts = [ { start = 20000, end = 20100 } ]

log.to = "console"
log.level = "info"
```

FRP Dashboard, Prometheus, and HTTP plugins are disabled. `FrpsConfiguration` owns the generated server TOML and every ycy-managed frps setting, so server configuration never accepts raw TOML or user-supplied file paths. Its first managed setting is the Custom 404 page: the Administrator Server view edits HTML code, while ycy atomically publishes `<data-dir>/404.html` beside `frps.toml`. Saving non-empty content takes effect on the next FRP-generated 404 response without restarting frps; clearing the editor removes the file and restores FRP's built-in page. This affects only 404 responses generated by FRP for unmatched routes or failed proxy requests, never a backend application's own 404 response.

The client configuration includes the advertised endpoint, Internal FRP Token, a stable hidden FRP user namespace, console logs at the resolved runtime level, and one proxy per enabled Tunnel Definition:

```toml
serverAddr = "tunnel.example.com"
serverPort = 7000
user = "ycy_<internal-client-key>"
loginFailExit = false

auth.method = "token"
auth.token = "<configured-or-generated-frp-token>"

log.to = "console"
log.level = "info"

[[proxies]]
name = "t_<stable-tunnel-id>"
type = "http"
localIP = "127.0.0.1"
localPort = 3000
customDomains = [ "app.example.com" ]
locations = [ "/service-a" ]
```

`customDomains` remains an array of aliases. A non-catch-all Tunnel Definition emits a one-element `locations` array, so every Tunnel Definition maps to one independently enabled FRP proxy. TCP and UDP proxies replace those fields with `remotePort`. If a snapshot has no enabled tunnels, the agent acknowledges it without keeping a frpc child in memory.

Typed advanced options render per-proxy encryption, compression, bandwidth limits, Proxy Protocol, TCP/HTTP health checks, HTTP Basic Auth, Host Header Rewrite, and request/response header sets. Basic Auth passwords remain recoverable for agent snapshots and generated client configuration, but browser responses expose only the username and a configured marker. The server data directory, backups, and client state directory must therefore be protected as sensitive data.

The typed capability scope and the reasons for deferring advanced FRP features are recorded in [FRP-CAPABILITIES.md](./FRP-CAPABILITIES.md). Configuration import accepts only the typed HTTP/TCP/UDP fields, ignores unsupported client and proxy settings, creates every selected definition disabled, and still applies route ownership, collision checks, snapshot compatibility, and rollback.

## Process Supervision

The server and each Client Connection Instance own zero or one FRP child. Child creation, exit handling, manual commands, and configuration reconciliation pass through one serialized state machine so concurrent events cannot start duplicates. The server opens its control HTTP listener first, then initializes its managed `frps` in the background and requires a 3-second activation confirmation before marking it `running` or issuing Agent configuration. A binary, configuration, port-conflict, or startup failure leaves `frps` `stopped` with a visible error while the control plane and state lock remain available; Server UI `Start` and `Restart` retry the complete initialization. `frpc` keeps its shorter activation confirmation window.

Unexpected child exits retry after `1s, 2s, 4s, 8s, 15s, 30s`, capped at 30 seconds. A stable run resets the failure count. A deterministic verification or configuration error enters `configuration_failed` and waits for a new Desired Revision or explicit operator action instead of looping.

### Client Apply Transaction

1. Render the complete desired TOML to a temporary file.
2. Run the pinned `frpc verify -c <temporary-file>` as a bounded child process.
3. If verification fails, keep the existing child and Applied Revision unchanged.
4. Stop the current frpc gracefully and wait for bounded termination.
5. Atomically publish the candidate configuration and start one new frpc.
6. If startup fails, restore the previous file and child, then report the new revision as failed.
7. On success, persist and acknowledge the new Applied Revision.

This deliberately causes a brief interruption to every tunnel on that client when a valid configuration changes. No frpc Admin API or reload adapter is kept resident.

### Manual Control

- Server UI `Start`, `Stop`, and `Restart` affect only the ycy-managed frps. Start and Restart return success only after the 3-second activation confirmation; an immediate exit leaves the state `stopped` and returns an error. Intentional Stop suppresses crash recovery until Start, Restart, or supervisor restart.
- Client UI exposes only `Restart frpc`; tunnel start/stop is the per-tunnel Enabled switch.
- ycy supervisors never restart themselves. Docker, systemd, launchd, or the calling shell owns that lifecycle.

## Control Plane HTTP And UI

The ycy HTTP service owns the embedded React application, JSON interface, durable bounded account sessions, and agent WebSocket. The FRP Dashboard is not proxied or embedded.

The implementation exposes these versioned routes:

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/api/session` | Authenticate a Control Plane Account. |
| `GET` | `/api/session` | Confirm and refresh the current account session. |
| `DELETE` | `/api/session` | End the current account session. |
| `PUT` | `/api/session/password` | Change the current local account password and end all of its sessions. |
| `GET` | `/api/state` | Return the current account and scoped overview; Administrator responses also contain frps and deployment settings. |
| `GET\|POST` | `/api/accounts` | Administrator-only account list and creation. |
| `PATCH\|DELETE` | `/api/accounts/:id` | Administrator-only role change or empty-account deletion. |
| `PUT` | `/api/accounts/:id/password` | Administrator-only local account password reset. |
| `GET\|POST` | `/api/clients` | List clients or create one with an optional Client Remark and generate its Client Token. |
| `GET\|PATCH\|DELETE` | `/api/clients/:id` | Read, change the Client Remark, or cascade-delete one internal client record. |
| `POST` | `/api/clients/:id/rotate` | Replace and reveal the Client Token. |
| `POST` | `/api/clients/:id/restart` | Ask an online client to restart frpc. |
| `GET\|POST` | `/api/clients/:id/tunnels` | List or create Tunnel Definitions. |
| `POST` | `/api/clients/:id/tunnels/import/preview` | Parse a bounded TOML configuration into selectable, redacted tunnel candidates. |
| `POST` | `/api/clients/:id/tunnels/import` | Atomically create selected candidates as disabled Tunnel Definitions. |
| `PATCH\|DELETE` | `/api/tunnels/:id` | Edit, enable/disable, or delete a tunnel. |
| `POST` | `/api/server/frp/:action` | Start, stop, or restart frps. |
| `GET\|PUT` | `/api/server/frps/config/custom-404-page` | Administrator-only managed Custom 404 page read and update. |
| WebSocket | `/api/agent` | Authenticated native agent control channel. |
| `GET` | `/healthz` | ycy liveness without FRP polling. |

The UI is deliberately limited to:

- Account login and local-account password change.
- A scoped Overview for every account; only Administrators see global frps state and deployment settings.
- Client list with Client Remarks, create/edit, token reveal/copy/rotate, delete, connection state, revision state, and an Administrator-only owner column.
- Client detail with HTTP/TCP/UDP tunnel CRUD, bounded configuration import preview and selection, one row per independently enabled proxy, expandable typed FRP options, last structured error, and frpc restart.
- Administrator Accounts view with create, role change, password reset, and empty-account deletion.
- Administrator Server view with frps controls, read-only deployment settings, and the managed Custom 404 page editor.

Structured collections such as Custom Domains and HTTP headers use repeatable input rows rather than multiline text. Binary settings use accessible Switch controls. Initial loads, background refreshes, and every explicit mutation expose local pending and error state; successful explicit mutations use bounded notifications, while passive event refreshes remain silent.

Do not add traffic charts, endpoint status, log viewers, onboarding marketing content, nested dashboard cards, or background polling for FRP state. Push client state changes over the existing server-to-browser event mechanism selected during implementation, and keep retained state bounded.

## Status Semantics

Client connection state is `connected`, `disconnected`, `incompatible`, or `revocation_pending`. FRP child state is `stopped`, `running`, `recovering`, or `configuration_failed`.

Tunnel presentation derives from configuration and revision state only:

| State | Meaning |
| --- | --- |
| `Disabled` | The definition reserves its resource but is omitted from FRP config. |
| `Pending` | Enabled, but the owning client has not applied the Desired Revision. |
| `Applied` | Included in the currently Applied Revision while frpc is running. |
| `Error` | The desired snapshot failed verification or activation. |

There is no `Healthy`, `Unhealthy`, throughput, or connection-count state.

## Docker Deployment

One multi-architecture Linux x64/arm64 image is published. It contains the standalone ycy binary and pinned FRP distribution, with `ycy` as its entrypoint but no default subcommand. Tunnel-specific data storage, ports, and the `tunnel server` command belong to the deployment configuration. No separate client image or client-container documentation is maintained.

A representative deployment is started with `docker compose -f docker-compose.tunnel.yml up -d` and publishes:

```yaml
services:
  tunnel:
    image: ghcr.io/hackycy/hackycy-cli:<version>
    restart: unless-stopped
    command: [tunnel, server]
    environment:
      YCY_TUNNEL_DATA_DIR: /data
      YCY_TUNNEL_ADMIN_USER: admin
      YCY_TUNNEL_ADMIN_PASSWORD: '${YCY_TUNNEL_ADMIN_PASSWORD:?required}'
    volumes:
      - tunnel-data:/data
    ports:
      - '7000:7000/tcp'
      - '8080:8080/tcp'
      - '7500:7500/tcp'
      - '20000-20100:20000-20100/tcp'
      - '20000-20100:20000-20100/udp'
```

NPM should normally expose the control UI through a dedicated HTTPS hostname and proxy each externally managed HTTP hostname to port 8080 while retaining its Host header. The FRP bind port and TCP/UDP pool must be published directly through Docker and the host firewall. `ycy tunnel server` is the only service that may listen on the configured FRP bind and HTTP vhost ports. If startup reports a conflict, stop the old standalone `frps` or other listener, then inspect the configured ports with `lsof -nP -iTCP:<port> -sTCP:LISTEN` or `ss -ltnp 'sport = :<port>'`.

When upgrading from a deployment with a separate `frps`, retain the ycy data directory or Docker volume, stop and remove the old `frps` service, then start the upgraded ycy tunnel service. Retaining the data directory preserves client enrollment and the generated Internal FRP Token when `YCY_TUNNEL_FRP_TOKEN` is not set.

## Source Ownership

```text
src/commands/tunnel/
  index.ts                 Commander registration only
  atomic-file.ts           Shared atomic state/configuration publication
  backoff.ts               Shared capped retry schedule
  config.ts                CLI/environment/secret-file parsing and validation
  paths.ts                 Fixed native and Docker state/binary paths
  lock.ts                  Cross-platform single-instance ownership
  types.ts                 Domain and control-protocol contracts
  frp/
    version.ts             Sole hand-maintained pinned FRP version
    manifest.generated.ts  Generated platform archives and checksums
    manifest.ts            Stable artifact resolution interface
    archive.ts             Archive extraction and digest primitives
    binary.ts              Resolve, download, verify, and atomically install
    toml.ts                TOML parse/stringify codec adapter
    config.ts              Typed frps/frpc document mapping and rendering
    supervisor.ts          Serialized zero-or-one child state machine
  server/
    run.ts                 Server composition and signal handling
    database.ts            SQLite schema and transactions
    control-plane.ts       Client/tunnel operations and revision truth
    agent-gateway.ts       WebSocket sessions and snapshot delivery
    frps-configuration.ts  Managed frps paths, TOML rendering, and typed settings
    tunnel-management.ts   Account sessions, authorization, ownership, projections, and administration
    views.ts               Client and tunnel status projections
    control-api.ts         Health and control API routes
    http.ts                Listener, embedded assets, SSE, and WebSocket lifecycle
    web/
      app.tsx              React application shell and page routing
      account-pages.tsx    Account administration workflows
      client-pages.tsx     Client list/detail and remark workflows
      ui.tsx               Shared operational UI controls
  client/
    run.ts                 Client composition and signal handling
    instance-state.ts      Per-connection locking and expired state cleanup
    agent.ts               WebSocket handshake, reconnect, and commands
    reconciler.ts          Verify, activate, rollback, and acknowledge
    state.ts               Last-applied rollback state
  acceptance.ts            Real-FRP HTTP/TCP/UDP forwarding acceptance
  idle-resource.test.ts    Bounded idle-state and no-polling acceptance
```

TunnelManagement is the only browser-facing owner of account authentication, authorization, ownership filtering, and administration. The control plane remains the owner of desired state and uniqueness behind that interface; the agent gateway uses its separate Client Token path. The reconciler is the only owner of client configuration activation. The supervisor is the only code allowed to spawn or stop FRP.

## Implementation Layers

1. Foundation: domain types, environment parsing, fixed paths, lock ownership, SQLite schema, FRP manifest/downloader, TOML rendering, and child supervision.
2. Server core: control-plane transactions, TunnelManagement accounts/authorization, frps composition, and the agent WebSocket handshake.
3. Native client: authentication-first startup, binary resolution, full-snapshot reconciliation, rollback, reconnect, cooperative revocation, and status acknowledgements.
4. UI: the accepted operational views, responsive layouts, visible mutation failures, and bounded state.
5. Distribution: native release binaries, the server Docker image, FRP license attribution, NPM/Docker examples, and release checksums.
6. Verification: unit and integration tests, real-FRP end-to-end acceptance, native build smoke checks, and idle-resource checks.

Foundation, server, native client, UI, and distribution remain independently testable. Control-plane and agent state machines are covered through direct tests and HTTP fixtures in addition to end-to-end acceptance.

## Verification And Acceptance

Required automated coverage:

- Configuration precedence, interactive saved-connection selection, encrypted multi-connection retention, opaque instance identities, secret files, Custom Domain and Location normalization, port ranges, and platform paths.
- SQLite account/ownership constraints, cascade deletion, token rotation, resource reservation, automatic port allocation, and atomic revision increments.
- Per-identity single-instance acquisition, distinct-instance concurrency, stale-lock handling, 90-day cleanup, one-child ownership, manual stop, backoff, and deterministic-error suppression.
- Binary artifact selection, SHA-256 rejection, atomic installation, manual-download diagnostics, and reported-version validation.
- Exact generated TOML for HTTP/TCP/UDP, disabled tunnels, stable names, and server port ranges.
- Agent authentication, duplicate rejection, compatibility rejection, complete snapshots, acknowledgements, reconnect, revoke, and revision races.
- Reconciler verification failure, activation rollback, empty enabled set, child crash recovery, and control outage behavior.
- Account authentication, role/owner matrices, session revocation, scoped SSE, and every mutating HTTP transaction.
- Real pinned-FRP end-to-end forwarding for multiple clients over HTTP Host routing, TCP, and UDP.
- Source-mode behavior and standalone builds on the supported native target matrix.

Acceptance scenarios:

1. A second server or Client Connection Instance cannot start against the same state directory; connections with a different origin or Token run concurrently.
2. Two trusted clients connect with distinct Tokens and receive only their own snapshots, including when launched from the same host state root.
3. Exact HTTP domain-location pairs and protocol-specific public ports cannot be double-reserved, including by disabled tunnels; different Locations on one domain can coexist.
4. A valid UI mutation automatically becomes Applied; an invalid snapshot leaves the previous revision running.
5. An ordinary management-link outage leaves existing forwarding active, but a cold-starting client does not use cache.
6. Token rotation preserves tunnels and invalidates the old agent cooperatively; deletion cascades tunnels and frees resources.
7. Unexpected FRP exits recover without duplicate children or a fast crash loop.
8. A missing client FRP binary downloads and verifies, while download failure prints a complete manual-install instruction.
9. Idle runtime performs no endpoint probes, FRP status polling, metric sampling, or log retention, and memory does not grow with traffic history.
10. Two User accounts see and mutate only their owned clients and tunnels, while an Administrator sees and operates all resources.
11. Role or password changes revoke all affected sessions, non-empty accounts cannot be deleted, and the Deployment Administrator remains environment-managed.

Before release, run the tunnel suites plus the repository-wide typecheck, lint, and standalone build. Changes to shared web primitives must also run existing `serve` and `diff` tests.
