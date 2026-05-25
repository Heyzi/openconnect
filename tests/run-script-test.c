/*
 * Test run_script() flag combinations.
 *
 * Exercises:
 *   SCRIPT_CAPTURE_OUTPUT   -- HIP path (stdout captured)
 *   SCRIPT_CAPTURE_OUTPUT + engine mapping -- mapping applied + output captured
 *   SCRIPT_REDIR_STDOUT     -- CSD path (stdout redirected to stderr)
 *
 * Requires PRINT_ARGS env var pointing to the print-args test binary.
 */
#define _GNU_SOURCE  /* for pipe2 */
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif
#ifdef _WIN32
#define SOCKET int
#endif
#include "../openconnect.h"
#include "../openconnect-internal.h"
#include "../textbuf.c"
#include "../script-engine.c"

static int failed;

static void check(const char *desc, int got, int want)
{
	if (got == want)
		fprintf(stderr, "ok: %s\n", desc);
	else {
		fprintf(stderr, "FAIL: %s: got %d, want %d\n", desc, got, want);
		failed++;
	}
}

/* stubs for functions not under test */
#undef vpn_progress
void vpn_progress(struct openconnect_info *vpninfo, int level, const char *fmt, ...)
{
	(void)vpninfo; (void)level; (void)fmt;
}

int set_csd_user(struct openconnect_info *vpninfo)
{
	(void)vpninfo;
	return 0;
}

#undef openconnect_utf8_to_legacy
char *openconnect_utf8_to_legacy(struct openconnect_info *vpninfo, const char *utf8)
{
	(void)vpninfo;
	return (char *)utf8;
}

#include "../script.c"

int main(void)
{
	struct openconnect_info vpninfo = { 0 };
	struct oc_text_buf *output;
	const char *script_argv[4];
	struct oc_vpn_option engine = { .option = ".py", .value = "python3", .next = NULL };
	const char *print_args;
	char pypath[256];
	int ret;

	print_args = getenv("PRINT_ARGS");
	if (!print_args) {
		fprintf(stderr, "SKIP: PRINT_ARGS not set\n");
		return 77;  /* automake skip */
	}

	/* test 1: direct exec -- script with shebang runs via execvp */
	script_argv[0] = print_args;
	script_argv[1] = NULL;
	ret = run_script(&vpninfo, script_argv, 0, NULL);
	check("direct exec exits 0", ret, 0);

	/* test 2: SCRIPT_CAPTURE_OUTPUT -- output captured from print-args */
	output = buf_alloc();
	if (!output) return 1;
	script_argv[0] = print_args;
	script_argv[1] = "testarg";
	script_argv[2] = NULL;
	ret = run_script(&vpninfo, script_argv, SCRIPT_CAPTURE_OUTPUT, output);
	check("SCRIPT_CAPTURE_OUTPUT exits 0", ret, 0);
	if (output->pos > 0 && strstr(output->data, "testarg"))
		fprintf(stderr, "ok: SCRIPT_CAPTURE_OUTPUT captured output\n");
	else {
		fprintf(stderr, "FAIL: SCRIPT_CAPTURE_OUTPUT: expected 'testarg' in output\n");
		failed++;
	}
	buf_free(output);

	/* test 3: engine mapping + SCRIPT_CAPTURE_OUTPUT -- python3 invoked for .py */
	vpninfo.script_engines = &engine;
	snprintf(pypath, sizeof(pypath), "/tmp/run-script-test-%d.py", (int)getpid());
	{
		FILE *f = fopen(pypath, "w");
		if (!f) {
			fprintf(stderr, "SKIP: could not create %s\n", pypath);
			goto skip_python;
		}
		fprintf(f, "#!/usr/bin/env python3\nimport sys\nprint(' '.join(sys.argv))\n");
		fclose(f);
		chmod(pypath, 0755);
	}
	output = buf_alloc();
	if (!output) return 1;
	script_argv[0] = pypath;
	script_argv[1] = "pyarg";
	script_argv[2] = NULL;
	ret = run_script(&vpninfo, script_argv, SCRIPT_CAPTURE_OUTPUT, output);
	check("engine mapping + SCRIPT_CAPTURE_OUTPUT exits 0", ret, 0);
	if (output->pos > 0 && strstr(output->data, "pyarg"))
		fprintf(stderr, "ok: engine mapping invoked python3 and captured output\n");
	else {
		fprintf(stderr, "FAIL: engine mapping: expected 'pyarg' in output\n");
		failed++;
	}
	buf_free(output);
	vpninfo.script_engines = NULL;
	unlink(pypath);
skip_python:

	/* test 4: SCRIPT_REDIR_STDOUT -- stdout goes to stderr, not captured */
	script_argv[0] = print_args;
	script_argv[1] = NULL;
	ret = run_script(&vpninfo, script_argv, SCRIPT_REDIR_STDOUT, NULL);
	check("SCRIPT_REDIR_STDOUT exits 0", ret, 0);

	return failed ? 1 : 0;
}
