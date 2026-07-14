# OpenConnect Desktop for macOS

This directory contains the first vertical prototype of the desktop product
described in the project specification. It intentionally remains isolated from
the upstream OpenConnect C build.

## Implemented

- agent process with graceful shutdown;
- random IPv4 loopback portal address;
- one-time bootstrap URL, HttpOnly session cookie, CSRF, Host and Origin checks;
- embedded, offline web portal;
- versioned profile storage with `0600` file permissions;
- profile create/read/update/delete API;
- root-only helper with an allow-listed Unix-socket protocol;
- managed OpenConnect lifecycle inside the helper;
- post-`vpnc-script` route capture from `CISCO_SPLIT_INC/EXC` and `TUNDEV`;
- system route add/delete/replace, with replace rollback on failure;
- system PAC proxy snapshot, apply, and restore on disconnect;
- password and OTP submission through child stdin only (never argv or profiles);
- bounded, secret-redacting log buffer and SSE stream;
- macOS Keychain adapter for secrets;
- runtime route table populated from OpenConnect server-route events;
- add, edit, and delete operations for server and user routes;
- downloadable ZIP diagnostics with redacted logs and secret-free profiles;
- `.app` and `.dmg` packaging scripts;
- placeholder helper binary whose interface deliberately accepts no commands.

## Run the prototype

```sh
go run ./cmd/openconnect-desktop -openconnect /path/to/openconnect
```

The bootstrap URL is printed once and opened in the default browser. The portal
requests both the account password and OTP for each connection. Both are passed
to OpenConnect through stdin and immediately discarded. Fully dynamic auth
forms (group selection, password changes, banners, and browser SAML) and the
privileged helper remain release-critical work for the next milestone.

Route edits are kept in memory for the active connection and are applied by the
root helper only after the real `vpnc-script` has successfully installed the
server configuration. The helper never accepts arbitrary commands or arbitrary
OpenConnect arguments.

## Build an app bundle

Build or provide a self-contained OpenConnect binary and `vpnc-script`, then:

```sh
OPENCONNECT_BINARY=/path/to/openconnect \
VPNC_SCRIPT=/path/to/vpnc-script \
./packaging/build-app.sh
./packaging/build-dmg.sh
```

The result is written to `dist/`. A distributable build must additionally copy
and rewrite all dylib dependencies, include third-party notices, codesign every
binary, enable hardened runtime, and be notarized.

## Security boundary

The portal and desktop agent run as the logged-in user. A macOS administrator
prompt starts only the bundled helper as root. Its socket is owned by that user
with mode `0600`, and its request schema is restricted to connection lifecycle
and validated route mutations. A release build should install the same helper
through signed SMAppService/XPC instead of the prototype authorization prompt.

## Verification

```sh
go test ./...
go vet ./...
```

Unit tests cover profile persistence, restrictive permissions, runtime route
validation and capture, portal sessions, CSRF, Host validation, log retention, secret
redaction, and diagnostic archive generation.
