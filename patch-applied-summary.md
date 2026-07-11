# Patch Application Summary

## Applied Patch: user-configurable-globalprotect-app-version.patch

This patch has been successfully applied to add support for a user-configurable GlobalProtect app version option.

## Changes Made

### 1. openconnect-internal.h
- Added `char *gp_app_version;` field to the `openconnect_info` struct
- Placed after CSD-related fields for logical grouping

### 2. main.c
- Added `OPT_GP_APP_VERSION` to the option enum
- Added `OPTION("gp-app-version", 1, OPT_GP_APP_VERSION)` to long_options array
- Added case handler `case OPT_GP_APP_VERSION:` that assigns `vpninfo->gp_app_version = keep_config_arg();`
- Added usage documentation: `"--gp-app-version=VERSION    Report GlobalProtect app version VERSION"`

### 3. gpst.c
- Modified the `append_opt(request_body, "app-version", ...)` call to use new fallback logic:
  - First priority: `vpninfo->csd_ticket` (existing behavior)
  - Second priority: `vpninfo->gp_app_version` (new user-configurable option)
  - Default fallback: `"6.3.0"` (updated from previous default of `"6.3.0-33"`)

### 4. openconnect.8.in
- Added man page documentation for the new `--gp-app-version=VERSION` option
- Includes description, use cases, common version values, and default behavior

## Functionality

The patch adds a new command-line option `--gp-app-version=VERSION` that allows users to specify the GlobalProtect client application version reported to the server. This is useful because:

1. Some servers require minimum client versions (e.g., 6.1.4 or higher)
2. Different versions may have different feature support
3. Some servers may reject connections from "outdated" clients

## Priority Order for App Version Selection

1. If `csd_ticket` is set (from portal response), use that value
2. If `--gp-app-version` was specified by user, use that value
3. Otherwise, use default value "6.3.0"

## Common Version Values

- 6.1.4
- 6.2.0
- 6.3.0 (default)
- 6.3.3

## Usage Example

```bash
openconnect --protocol=gp --gp-app-version=6.1.4 vpn.example.com
```

This patch maintains backward compatibility while providing users the flexibility to specify client version when needed for server compatibility.