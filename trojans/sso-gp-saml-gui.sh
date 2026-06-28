#!/bin/sh
# --sso-wrapper helper for GlobalProtect on Linux: a thin adapter around
# gp-saml-gui <https://github.com/dlenski/gp-saml-gui>, which runs the SAML
# webview login and prints HOST/USER/COOKIE/OS. openconnect invokes us as
#   sso-gp-saml-gui.sh <login-url> <gateway>
# and reads passwd=/user=/usergroup= back. Needs gp-saml-gui on PATH and a
# graphical (GTK) session.
gateway=$2
[ -n "$gateway" ] || { echo "$0: no gateway supplied" >&2; exit 1; }

# gp-saml-gui prints shell-quoted HOST=/USER=/COOKIE=/OS= on stdout (its
# documented eval form); progress goes to stderr. Clear first so a field it
# omits reads as empty, not as an inherited value (e.g. $USER).
out=$(gp-saml-gui --gateway "$gateway") || exit 1
unset HOST USER COOKIE OS
eval "$out"
[ -n "$COOKIE" ] || { echo "$0: gp-saml-gui returned no cookie" >&2; exit 1; }

printf 'passwd=%s\nuser=%s\nusergroup=gateway:prelogin-cookie\n' "$COOKIE" "$USER"
