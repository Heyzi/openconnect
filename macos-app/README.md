# OpenConnect Desktop for macOS

This directory contains the first vertical prototype of the desktop product
described in the project specification. It intentionally remains isolated from
the upstream OpenConnect C build.

## Implemented

- agent process with graceful shutdown;
- random IPv4 loopback portal address;
- one-time secret bootstrap URL with a revocable HttpOnly session cookie, CSRF, Host and Origin checks;
- embedded, offline web portal;
- versioned profile storage with `0600` file permissions;
- profile create/read/update/delete API;
- root-only helper with an allow-listed Unix-socket protocol;
- managed OpenConnect lifecycle inside the helper;
- disconnect completion only after both OpenConnect exit and `vpnc-script` state-file removal, with explicit recovery from stale network state;
- desktop-only AnyConnect user-agent and compression defaults passed by the helper, leaving the upstream CLI defaults unchanged;
- automatic OpenConnect reconnect and route refresh after macOS wake;
- advisory DNS/TCP server probing for fallback selection that never blocks an OpenConnect attempt;
- post-`vpnc-script` route capture from `CISCO_SPLIT_INC/EXC` and `TUNDEV`;
- system route add/delete/replace, with replace rollback on failure;
- system PAC proxy snapshot, apply, and restore on disconnect;
- password and OTP submission through child stdin only (never argv or profiles);
- bounded, secret-redacting log buffer and SSE stream;
- macOS Keychain adapter for secrets;
- runtime route table populated from OpenConnect server-route events;
- add, edit, and delete operations for server and user routes;
- downloadable ZIP diagnostics with redacted logs and secret-free profiles;
- optional capture of the exact server-provided CSD payload in a private temporary directory, with a Finder shortcut and cleanup on the next launch;
- connection inspector with staged DNS/TCP, TLS, authentication, posture, tunnel, UDP, and route state;
- automatic DNS, TCP, TLS certificate, tunnel, route, and posture diagnostics after every successful connection;
- Posture/HostScan center with request detection and captured payload size and SHA-256 metadata;
- `.app` and `.dmg` packaging scripts.

## Run the prototype

```sh
go run ./cmd/openconnect-desktop -openconnect /path/to/openconnect
```

The one-time bootstrap URL is printed on startup and opened in the default browser. The tray
opens the token-free portal URL and relies on the existing browser session. The portal
requests both the account password and OTP for each connection. Both are passed
to OpenConnect through stdin and immediately discarded. Fully dynamic auth
forms (group selection, password changes, banners, and browser SAML) remain
release-critical work for the next milestone.

Route edits are kept in memory for the active connection and are applied by the
root helper only after the real `vpnc-script` has successfully installed the
server configuration. The helper never accepts arbitrary commands or arbitrary
OpenConnect arguments.

## Build a portable app bundle

Build OpenConnect in the repository root, then run:

```sh
./packaging/build-release.sh
```

The release command rebuilds the C client, runs the Go tests, rebuilds the app
and DMG, verifies both, and writes a SHA-256 checksum. The DMG filename includes
the source commit and a `-dirty` suffix when appropriate, for example
`OpenConnect-Desktop-295e77b4cbd8.dmg`. Set `BUILD_COMMIT` explicitly when
building from a source archive without Git metadata.

The script embeds the real OpenConnect executable, `libopenconnect`,
`vpnc-script`, and all non-system dylib dependencies in the application. It
fails instead of producing an incomplete bundle when any required component is
missing. Override `OPENCONNECT_BINARY`, `OPENCONNECT_LIBRARY`, or `VPNC_SCRIPT`
to use non-default build artifacts. The result is written to `dist/`; public
distribution still requires an Apple Developer ID signature and notarization.

## Security boundary

The portal and desktop agent run as the logged-in user. The bundled root helper
is registered with `SMAppService` and requires approval in System Settings.
Its Unix socket is owned by the console user with mode `0600`, and its request
schema is restricted to connection lifecycle and validated route mutations.

## Verification

```sh
go test ./...
go vet ./...
```

Unit tests cover profile persistence, restrictive permissions, runtime route
validation and capture, portal sessions, CSRF, Host validation, log retention, secret
redaction, diagnostic archive generation, and the process-plus-state-file cleanup invariant.
