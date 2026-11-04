# Network and sandbox behavior

How an embedder's `runtime.Config` applies to scripts. See the sandboxing example in the
[README](../README.md#sandboxing).

Workers and `module.register` loader realms created by an instance inherit that
instance's configuration, and are stopped when it is disposed. Instances do not share
configuration, so one process can host parents with different sandboxes.

## Network modules

`node:net`, `node:tls`, `node:http`, `node:https`, `node:http2` and `node:dgram` connect through the same injected controls:

- `Config.DNSResolver` resolves the name. `Config.SocketFactory` then connects to the
  resolved IP, so a check on that IP applies to the real destination and DNS cannot
  change it afterwards.
- `FilteredSocketFactory` evaluates the requested name and the resolved IP together. A
  custom `NetworkPolicy` can do the same by implementing `ResolvedAddressPolicy`;
  otherwise both must be allowed. A custom socket that decides by hostname can implement
  `ResolvedTCPSocket` or `ResolvedUDPSocket`; otherwise it receives only the IP.
- An address from a script's `lookup` option is checked as an address. The name it answered
  for is not trusted, so it cannot borrow an allowed name.

## HTTP clients

`http.request` and `https.request` open their own connections when `Config.HTTPClient` is
unset, or is a `FilteredHTTPClient` over the real client using the same built-in policy
object as a `FilteredSocketFactory`. Any other `HTTPClient` is your own and sees every
request as a whole: bodies are buffered, and `CONNECT`, upgrades, other protocol switches and
the `createConnection` option fail with `ERR_HTTP_TRANSPORT_UNAVAILABLE` rather than opening a
connection around it. `fetch` always uses `Config.HTTPClient`.

An `HTTPClient` governs HTTP only. `node:net` and `node:tls` use `SocketFactory`, so a script
can still reach anything the socket factory allows and speak HTTP itself. To restrict a
script's network access, restrict both.

`http2.connect` cannot use an `HTTPClient`. If you inject one but leave `SocketFactory` as
real sockets, it fails with `ERR_HTTP2_TRANSPORT_UNAVAILABLE` rather than connecting around
your restrictions.

## TLS

Certificates are verified as Node does: the chain, then the hostname. A failure closes the
connection with Node's error code, such as `DEPTH_ZERO_SELF_SIGNED_CERT` or
`ERR_TLS_CERT_ALTNAME_INVALID`.

- As in Node, scripts may change or skip this with `rejectUnauthorized`, `ca`,
  `checkServerIdentity` or `NODE_TLS_REJECT_UNAUTHORIZED`. Set `Config.ClientTLSConfig`
  (`&tls.Config{}` for the system roots) to make your TLS configuration authoritative for
  `node:tls`, `node:https` and `node:http2`.
- `VerifyPeerCertificate` and `VerifyConnection` in `Config.ClientTLSConfig` behave as they do
  in `crypto/tls`: they run after verification and receive its verified chains, and there are
  no chains if you set `InsecureSkipVerify`. `VerifyPeerCertificate` is not called on
  resumed connections.
- TLS runs on top of a socket from `SocketFactory`. `SocketFactory.CreateTLSSocket` is no
  longer called by these modules.
- `tls.createServer` and `https.createServer` are real TLS servers. Scripts supply their own
  keys, and listening is subject to the socket policy as usual.

## DNS

`dns.lookup` and `dns.resolve*` use `Config.DNSResolver`. Querying a server directly, which
`dns.resolveSoa`, `resolveCaa`, `resolveNaptr`, `resolvePtr`, `resolveAny` and TTLs need, is
available with the real resolver or one implementing `RecordResolver`, and otherwise fails
with `ENOTIMP`.

A script's `dns.setServers` choice is only honored with the real resolver and real sockets.
Under any other configuration it is recorded but not used, because a query to a server of
the script's choosing would be traffic your controls never saw.

## Unix domain sockets

Unix sockets reach local services through the filesystem, and a network policy knows nothing
about them. They work only with `RealSocketFactory` itself and an unrestricted filesystem,
and otherwise fail with `EACCES`. A type that embeds `RealSocketFactory` does not inherit
them. A custom factory can offer them by implementing `UnixSocketFactory`.
