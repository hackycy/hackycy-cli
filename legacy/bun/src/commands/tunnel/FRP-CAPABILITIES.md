# FRP v0.70.1 Capability Notes

This note records the FRP behavior that should shape the typed `ycy tunnel`
control-plane model. It is pinned to the official FRP v0.70.1 documentation and
source; it is not a promise to expose every FRP option.

## Decision Summary

The same-domain, different-path example in the issue is a native FRP use case.
An HTTP proxy expands every `customDomains` x `locations` combination into a
route, so several proxies can share `routes.example.com` when their locations are
different. FRP's own example routes `/news` and `/about` to one backend and `/`
to another backend on the same domain, and the pinned release tests this exact
shape. [Official URL-routing example](https://github.com/fatedier/frp/blob/v0.70.1/README.md#url-routing)
[v0.70.1 end-to-end test](https://github.com/fatedier/frp/blob/v0.70.1/test/e2e/v1/basic/http.go#L35-L81)

The control plane therefore reserves HTTP route keys rather than entire
hostnames. It models one optional Location per independently enabled Tunnel
Definition and renders that value as a zero- or one-element `locations` array.
Multiple Custom Domains remain aliases on the same definition. A raw TOML
escape hatch would bypass ownership, collision checking, snapshot validation,
secret handling, and deterministic rollback. The one-time configuration import
workflow is not such an escape hatch: it maps supported proxy fields into
disabled typed definitions before applying the normal control-plane checks.

## HTTP Routing Semantics

- `customDomains` and `locations` are both arrays. FRP registers their Cartesian
  product; omitting `locations` registers the empty prefix, which matches every
  path. [HTTP proxy registration](https://github.com/fatedier/frp/blob/v0.70.1/server/proxy/http.go#L55-L110)
- A route conflicts only when the lower-cased domain, exact location, and
  `routeByHTTPUser` are all equal. Repeating a domain with a different location
  is allowed. Repeated domains or locations inside one proxy must be rejected or
  de-duplicated before rendering because their Cartesian product would register
  the same route twice. [Router registration and conflict check](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/router.go#L36-L69)
- Matching is a literal, case-sensitive path-prefix test. The most specific
  matching prefix wins, independently of proxy or array order. Thus `/service`
  and `/service-a` may coexist, but `/service` also matches `/service-extra` and
  `/service-a` also matches `/service-a-extra`; this is not path-segment or regex
  matching. [Router match and ordering](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/router.go#L63-L65) [Prefix match](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/router.go#L107-L125)
- Routing uses `req.URL.Path`, so the query string does not participate. FRP
  changes routing host and selected headers but does not strip the matched
  location; the backend receives the original path. [HTTP request route data](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/http.go#L248-L265) [Reverse-proxy rewrite](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/http.go#L57-L101)
- Host matching is case-insensitive. v0.70.1's router also supports exact,
  increasingly broad wildcard, and catch-all domain precedence, with exact host
  first. Wildcard support should remain a separate product decision because it
  also needs hierarchical reservations and external wildcard DNS/TLS ownership.
  [Domain lookup precedence](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/router.go#L127-L155)

Recommended control-plane invariants:

1. Normalize and reserve every `(exact hostname, location)` pair globally,
   including disabled tunnels. Overlapping prefixes are valid; exact duplicate
   pairs are not.
2. Require each location to start with `/`, preserve case and trailing slash,
   and store at most one per Tunnel Definition. Preserve an omitted value as
   FRP's distinct empty-prefix catch-all rather than rewriting it to `/`.
3. Normalize and de-duplicate domains case-insensitively before producing the
   domain-location product.
4. Explain in field-level validation that locations select a backend but do not
   rewrite or remove the request path.

With those rules, the configuration from the issue should render unchanged and
the four HTTP routes can coexist. Requests outside those four prefixes receive
FRP's no-route response unless an additional `/` catch-all is defined.

## Collision Model

| Proxy type | FRP routing or reservation key | Consequence for `ycy tunnel` |
| --- | --- | --- |
| HTTP | Lower-cased domain + exact location + `routeByHTTPUser` | Reserve domain + location while `routeByHTTPUser` stays unsupported. Allow overlapping prefixes. |
| HTTPS | SNI domain; there is no location field | Reserve the exact domain globally. Configure a separate `vhostHTTPSPort`. |
| TCP | TCP `remotePort` | One owner per TCP port unless a load-balancer group is deliberately modeled. |
| UDP | UDP `remotePort` | One owner per UDP port. The same number may simultaneously be used by TCP because FRP has separate TCP and UDP port managers. |

Sources: [HTTP and HTTPS config shapes](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/proxy.go#L314-L392),
[HTTPS SNI extraction](https://github.com/fatedier/frp/blob/v0.70.1/pkg/util/vhost/https.go#L26-L50),
[port collision handling](https://github.com/fatedier/frp/blob/v0.70.1/server/ports/ports.go#L82-L145), and
[separate TCP/UDP managers](https://github.com/fatedier/frp/blob/v0.70.1/server/service.go#L165-L179).

## Typed Capability Scope

The control-plane interface exposes the stable routing core and common
operational options as typed fields. FRP proxy names remain generated from
stable tunnel IDs; the user-facing label does not become FRP identity.

| Scope | Current first-class fields | Deferred candidates |
| --- | --- | --- |
| All proxies | `enabled`, `localIP`, `localPort`, encryption, compression, bandwidth limit, Proxy Protocol, TCP/HTTP health checks | Annotations and plugin metadata. |
| HTTP | Custom Domain aliases, one optional Location, `hostHeaderRewrite`, Basic Auth, request/response header `set` maps | `routeByHTTPUser`, subdomains, wildcard domains, and load-balancer groups. |
| HTTPS | Not supported; public TLS currently terminates before the FRP HTTP listener. | A separate `vhostHTTPSPort`, SNI domain reservation, TLS backend contract, and optional Proxy Protocol. |
| TCP | `remotePort`, `localIP`, `localPort` | Optional Proxy Protocol. Do not expose `remotePort = 0`, which bypasses deterministic reservation. |
| UDP | `remotePort`, `localIP`, `localPort` | Optional Proxy Protocol for explicitly compatible backends. |

HTTP and HTTPS require at least one `customDomains` entry or `subdomain`; the
server separately requires the corresponding vhost listener. Header operations
in this release only support `set`, so the control plane should not invent a
header-delete operation. [Domain validation](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/validation/proxy.go#L74-L99)
[Header operation shape](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/common.go#L133-L140)

The underlying v0.70.1 base config supports the implemented per-proxy
encryption, compression, bandwidth limit and limit mode, Proxy Protocol v1/v2,
and TCP/HTTP health checks. Defaults remain off, and HTTP health checks require
a path. [Base, transport, and health-check fields](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/proxy.go#L28-L131) [Validation rules](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/validation/proxy.go#L25-L59)

For HTTP, FRP officially documents `hostHeaderRewrite`, request/response header
sets, Basic Auth, health checks, and load balancing. [HTTP headers and host rewrite](https://github.com/fatedier/frp/blob/v0.70.1/README.md#rewriting-the-http-host-header)
[HTTP Basic Auth](https://github.com/fatedier/frp/blob/v0.70.1/README.md#require-http-basic-auth-password-for-web-services)
[health checks](https://github.com/fatedier/frp/blob/v0.70.1/README.md#service-health-check)

Supporting native `https` requires a server `vhostHTTPSPort` option/listener.
FRP rejects HTTPS proxies when that listener is disabled. This is different from
terminating public TLS in NPM and forwarding plain HTTP to FRP: the latter must
remain an `http` tunnel and retains locations and HTTP header features.
[HTTPS listener requirement](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/validation/proxy.go#L213-L226)
[HTTPS listener config](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/server.go#L43-L65)

## Deliberately Deferred FRP Features

These should not be represented as free-form fields or raw config. They can be
added later as typed domain concepts when there is a concrete workflow. When
encountered during configuration import, they are reported as ignored rather
than being persisted or passed through to FRP.

- **Load-balancer groups (`loadBalancer.group`/`groupKey`)**: useful, but they
  intentionally make several proxies share one HTTP route or TCP port. Correct
  support needs a service/backend model, group-wide validation, cross-client
  lifecycle semantics, health state, and secret handling. Passing the two FRP
  strings through would break the current reservation model. FRP also requires
  grouped HTTP routes to have identical domains and locations.
  [Official load-balancing constraints](https://github.com/fatedier/frp/blob/v0.70.1/README.md#load-balancing)
- **`routeByHTTPUser`**: this routes on the Basic Auth username and changes the
  HTTP collision key. It is niche, easy to confuse with access control, and
  complicates secret redaction and route ownership.
  [HTTP route config](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/proxy.go#L316-L340)
- **`subdomain`/server `subDomainHost` and wildcard domains**: these require a
  server-wide DNS namespace, wildcard ingress/TLS, and overlap-aware ownership.
  Exact `customDomains` cover the requested workflows without those deployment
  dependencies. [Subdomain server contract](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/server.go#L61-L65)
- **Client plugins**, including `https2http`, `https2https`, `static_file`,
  Unix sockets, SOCKS, and HTTP proxy: a plugin replaces `localIP`/`localPort`
  and brings plugin-specific files, certificates, credentials, and schemas.
  Supporting a generic plugin blob would defeat typed snapshot verification.
  [Plugin replaces the normal backend](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/proxy.go#L59-L67)
- **STCP/XTCP/SUDP proxies and visitors**: these are private peer-to-peer or
  NAT-traversal workflows that require two coordinated client roles, secrets,
  and visitor resources. They are not public HTTP/HTTPS/TCP/UDP tunnel variants.
  [Proxy type registry](https://github.com/fatedier/frp/blob/v0.70.1/pkg/config/v1/proxy.go#L214-L239)
- **TCP multiplexing, server plugins, alternate KCP/QUIC transports,
  includes/templates, dashboard, and raw FRP config injection**: these change
  server topology or create a second management/configuration plane. They should
  remain outside the tunnel CRUD surface until separately designed and tested.

## Implementation Checkpoints

Before considering the capability complete, verify these cases with the pinned
`frpc verify` command and focused control-plane tests:

1. The issue's four same-domain HTTP proxies plus the TCP proxy render and pass
   `frpc verify` on v0.70.1.
2. `/service-a/x` reaches port 9001, while `/service-b/x` reaches 9101;
   unmatched paths return no route unless `/` exists.
3. Exact duplicate domain-location products are rejected before snapshot apply,
   while nested and sibling prefixes are accepted.
4. Domain duplicates differing only by case are de-duplicated, and each proxy
   contains at most one Location.
5. If HTTPS is added, startup renders `vhostHTTPSPort`, SNI routes to a TLS
   backend, and the UI does not offer HTTP-only fields for HTTPS.
