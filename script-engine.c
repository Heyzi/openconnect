/*
 * OpenConnect (SSL + DTLS) VPN client
 *
 * Copyright © 2008-2015 Intel Corporation.
 *
 * Author: David Woodhouse <dwmw2@infradead.org>
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of the GNU Lesser General Public License
 * version 2.1, as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Lesser General Public License for more details.
 */

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif
#ifdef _WIN32
#define SOCKET int
#endif
#include "openconnect.h"
#include <string.h>
#include <strings.h>

/* declaration provided in openconnect-internal.h for callers;
 * repeated here to satisfy -Wmissing-declarations */
const char *script_engine(struct oc_vpn_option *engines, const char *path);

const char *script_engine(struct oc_vpn_option *engines, const char *path)
{
	const char *dot = strrchr(path, '.');
	struct oc_vpn_option *e;

	/* check user-configured mappings first */
	if (dot) {
		for (e = engines; e; e = e->next) {
			if (!strcasecmp(dot, e->option))
				return e->value;
		}
	}

#ifdef _WIN32
	if (dot && !strcasecmp(dot, ".js"))
		return "cscript.exe /e:JScript";
	if (dot && !strcasecmp(dot, ".py"))
		return "python.exe";
	if (dot && !strcasecmp(dot, ".exe"))
		return NULL;  /* execute directly */
	return "cscript.exe";
#else
	if (dot && !strcasecmp(dot, ".sh"))
		return "/bin/sh";
	return NULL; /* execute directly; kernel handles shebang */
#endif
}
