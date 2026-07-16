# OpenConnect Desktop for macOS

This directory contains the first vertical prototype of the desktop product
described in the project specification. It intentionally remains isolated from
the upstream OpenConnect C build.

## Implemented

- agent process with graceful shutdown;
- random IPv4 loopback portal address;
- secret bootstrap URL with renewable HttpOnly session cookie, CSRF, Host and Origin checks;
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

The bootstrap URL is printed on startup and opened in the default browser. The tray
uses the same URL so it can restore the browser session after sleep or an agent restart. The portal
requests both the account password and OTP for each connection. Both are passed
to OpenConnect through stdin and immediately discarded. Fully dynamic auth
forms (group selection, password changes, banners, and browser SAML) and the
privileged helper remain release-critical work for the next milestone.

Route edits are kept in memory for the active connection and are applied by the
root helper only after the real `vpnc-script` has successfully installed the
server configuration. The helper never accepts arbitrary commands or arbitrary
OpenConnect arguments.

## Build a portable app bundle

Build OpenConnect in the repository root, then run:

```sh
./packaging/build-app.sh
./packaging/build-dmg.sh
```

The DMG filename includes the source commit, for example
`OpenConnect-Desktop-295e77b4cbd8.dmg`. Set `BUILD_COMMIT` explicitly when
building from a source archive without Git metadata.

The script embeds the real OpenConnect executable, `libopenconnect`,
`vpnc-script`, and all non-system dylib dependencies in the application. It
fails instead of producing an incomplete bundle when any required component is
missing. Override `OPENCONNECT_BINARY`, `OPENCONNECT_LIBRARY`, or `VPNC_SCRIPT`
to use non-default build artifacts. The result is written to `dist/`; public
distribution still requires an Apple Developer ID signature and notarization.

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
