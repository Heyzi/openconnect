/*
 * Test --script-engine mapping and script_engine() lookup.
 */
#include <stdio.h>
#include <string.h>
#ifdef _WIN32
#define SOCKET int
#endif
#include "../openconnect.h"
#include "../script-engine.c"

static int failed;

static void check(const char *desc, const char *got, const char *want)
{
	int match = (got == want) || (got && want && strcmp(got, want) == 0);
	if (match)
		fprintf(stderr, "ok: %s -> %s\n", desc, got ? got : "(null)");
	else {
		fprintf(stderr, "FAIL: %s: got '%s', want '%s'\n",
			desc, got ? got : "(null)", want ? want : "(null)");
		failed++;
	}
}

int main(void)
{
	struct oc_vpn_option *engines = NULL;
	struct oc_vpn_option e1 = { .option = ".py", .value = "python3", .next = NULL };
	struct oc_vpn_option e2 = { .option = ".js", .value = "qjs --std", .next = &e1 };

#ifdef _WIN32
	check(".js default",  script_engine(engines, "foo.js"),  "cscript.exe /e:JScript");
	check(".py default",  script_engine(engines, "foo.py"),  "python.exe");
	check(".exe default", script_engine(engines, "foo.exe"), NULL);
	check(".sh default",  script_engine(engines, "foo.sh"),  "cscript.exe");
	check("no ext",       script_engine(engines, "foo"),     "cscript.exe");
#else
	check(".sh default",  script_engine(engines, "foo.sh"),  "/bin/sh");
	check("no ext",       script_engine(engines, "foo"),     NULL);
	check(".py default",  script_engine(engines, "foo.py"),  NULL);
#endif

	engines = &e2;
	check(".py mapped",   script_engine(engines, "foo.py"),  "python3");
	check(".js mapped",   script_engine(engines, "foo.js"),  "qjs --std");
#ifdef _WIN32
	check(".sh unmapped", script_engine(engines, "foo.sh"),  "cscript.exe");
#else
	check(".sh unmapped", script_engine(engines, "foo.sh"),  "/bin/sh");
#endif

	return failed ? 1 : 0;
}
