#include <config.h>

#include "openconnect-internal.h"
#include "win32-background.h"

#ifdef _WIN32

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <wtypes.h>
#include <shellapi.h>

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <wchar.h>
#include <unistd.h>

#define OC_BG_COOKIE_ENV L"OPENCONNECT_BG_COOKIE"

static int background_child;
static char *background_child_event;
static int background_notified;

int win32_background_is_child(void)
{
	return background_child;
}

void win32_background_set_child_event(char *event_name)
{
	background_child = 1;
	background_child_event = event_name;
	background_notified = 0;
}

static wchar_t *utf8_to_wchar(const char *str)
{
	wchar_t *wstr;
	int chars;

	if (!str)
		return NULL;

	chars = MultiByteToWideChar(CP_UTF8, 0, str, -1, NULL, 0);
	if (!chars)
		return NULL;

	wstr = malloc(chars * sizeof(*wstr));
	if (!wstr)
		return NULL;

	if (!MultiByteToWideChar(CP_UTF8, 0, str, -1, wstr, chars)) {
		free(wstr);
		return NULL;
	}

	return wstr;
}

static char *wchar_to_utf8(const wchar_t *wstr)
{
	char *str;
	int bytes;

	if (!wstr)
		return NULL;

	bytes = WideCharToMultiByte(CP_UTF8, 0, wstr, -1, NULL, 0, NULL, NULL);
	if (!bytes)
		return NULL;

	str = malloc(bytes);
	if (!str)
		return NULL;

	if (!WideCharToMultiByte(CP_UTF8, 0, wstr, -1, str, bytes, NULL, NULL)) {
		free(str);
		return NULL;
	}

	return str;
}

static int win32_setenv_utf8(const wchar_t *name, const char *value)
{
	int ret = 0;
	wchar_t *wval;

	if (!value)
		return SetEnvironmentVariableW(name, NULL) ? 0 : -1;

	wval = utf8_to_wchar(value);
	if (!wval)
		return -1;

	if (!SetEnvironmentVariableW(name, wval))
		ret = -1;

	free(wval);
	return ret;
}

static char *win32_getenv_utf8(const wchar_t *name)
{
	DWORD len;
	wchar_t *wbuf;
	char *utf8;

	len = GetEnvironmentVariableW(name, NULL, 0);
	if (!len)
		return NULL;

	wbuf = malloc(len * sizeof(*wbuf));
	if (!wbuf)
		return NULL;

	if (!GetEnvironmentVariableW(name, wbuf, len)) {
		free(wbuf);
		return NULL;
	}

	utf8 = wchar_to_utf8(wbuf);
	free(wbuf);
	return utf8;
}

int win32_unlink_utf8(const char *path)
{
	wchar_t *wpath;

	if (!path)
		return -1;

	wpath = utf8_to_wchar(path);
	if (!wpath)
		return unlink(path);

	if (!DeleteFileW(wpath)) {
		free(wpath);
		return -1;
	}

	free(wpath);
	return 0;
}

static wchar_t *quote_arg_win32(const wchar_t *arg)
{
	size_t len, cap, i;
	wchar_t *out;
	wchar_t *p;
	size_t bs = 0;

	if (!arg) {
		out = malloc(3 * sizeof(*out));
		if (!out)
			return NULL;
		out[0] = L'"';
		out[1] = L'"';
		out[2] = 0;
		return out;
	}

	len = wcslen(arg);
	cap = (len * 2) + 3;
	out = malloc(cap * sizeof(*out));
	if (!out)
		return NULL;

	p = out;
	*p++ = L'"';

	for (i = 0; i < len; i++) {
		wchar_t c = arg[i];
		if (c == L'\\') {
			bs++;
			*p++ = L'\\';
			continue;
		}
		if (c == L'"') {
			while (bs) {
				*p++ = L'\\';
				bs--;
			}
			bs = 0;
			*p++ = L'\\';
			*p++ = L'"';
			continue;
		}
		bs = 0;
		*p++ = c;
	}

	while (bs) {
		*p++ = L'\\';
		bs--;
	}

	*p++ = L'"';
	*p = 0;
	return out;
}

static int should_skip_bg_arg(const wchar_t *arg)
{
	static const wchar_t bg_child_prefix[] = L"--background-child=";

	if (!arg)
		return 0;
	if (!wcscmp(arg, L"--cookie-on-stdin"))
		return 1;
	if (!wcscmp(arg, L"--passwd-on-stdin"))
		return 1;
	if (!wcscmp(arg, L"--background-child"))
		return 1;
	if (!wcsncmp(arg, bg_child_prefix, ARRAY_SIZE(bg_child_prefix) - 1))
		return 1;
	return 0;
}

static int append_wstr(wchar_t **buf, size_t *len, size_t *cap, const wchar_t *add)
{
	size_t add_len;
	wchar_t *new_buf;

	add_len = wcslen(add);
	if (*len + add_len + 1 > *cap) {
		size_t new_cap = (*cap ? *cap * 2 : 256);
		while (*len + add_len + 1 > new_cap)
			new_cap *= 2;

		new_buf = realloc(*buf, new_cap * sizeof(*new_buf));
		if (!new_buf)
			return -1;

		*buf = new_buf;
		*cap = new_cap;
	}

	memcpy(*buf + *len, add, add_len * sizeof(*add));
	*len += add_len;
	(*buf)[*len] = 0;
	return 0;
}

static wchar_t *build_bg_command_line(int argc_w, wchar_t **argv_w, const wchar_t *extra_arg)
{
	wchar_t *cmd = NULL;
	size_t len = 0, cap = 0;
	int i;

	for (i = 0; i < argc_w; i++) {
		wchar_t *quoted;

		if (should_skip_bg_arg(argv_w[i]))
			continue;

		quoted = quote_arg_win32(argv_w[i]);
		if (!quoted)
			goto err;

		if (len && append_wstr(&cmd, &len, &cap, L" ") < 0) {
			free(quoted);
			goto err;
		}
		if (append_wstr(&cmd, &len, &cap, quoted) < 0) {
			free(quoted);
			goto err;
		}
		free(quoted);
	}

	if (extra_arg) {
		wchar_t *quoted = quote_arg_win32(extra_arg);
		if (!quoted)
			goto err;
		if (len && append_wstr(&cmd, &len, &cap, L" ") < 0) {
			free(quoted);
			goto err;
		}
		if (append_wstr(&cmd, &len, &cap, quoted) < 0) {
			free(quoted);
			goto err;
		}
		free(quoted);
	}

	return cmd;

err:
	free(cmd);
	return NULL;
}

void win32_background_apply_cookie(struct openconnect_info *vpninfo)
{
	char *cookie;

	if (!background_child)
		return;

	cookie = win32_getenv_utf8(OC_BG_COOKIE_ENV);
	if (cookie) {
		if (!vpninfo->cookie)
			vpninfo->cookie = cookie;
		else
			free(cookie);
	}

	win32_setenv_utf8(OC_BG_COOKIE_ENV, NULL);
}

int win32_spawn_background_child(int argc, char **argv, struct openconnect_info *vpninfo)
{
	wchar_t event_name_w[64];
	wchar_t extra_arg[128];
	wchar_t *cmdline = NULL;
	wchar_t **argv_w = NULL;
	int argc_w = 0;
	HANDLE evt = NULL;
	PROCESS_INFORMATION pi;
	STARTUPINFOW si;
	DWORD wait_ret;
	DWORD exit_code = 1;
	DWORD create_flags;
	BOOL ok;

	(void)argc;
	(void)argv;

	if (!vpninfo->cookie) {
		fprintf(stderr, _("No cookie available for background process\n"));
		return 1;
	}

	swprintf(event_name_w, ARRAY_SIZE(event_name_w),
		 L"OpenConnectBg-%lu-%lu",
		 (unsigned long)GetCurrentProcessId(),
		 (unsigned long)GetTickCount());

	evt = CreateEventW(NULL, TRUE, FALSE, event_name_w);
	if (!evt) {
		char *errstr = openconnect__win32_strerror(GetLastError());
		fprintf(stderr, _("CreateEvent() failed: %s\n"), errstr);
		free(errstr);
		return 1;
	}

	if (win32_setenv_utf8(OC_BG_COOKIE_ENV, vpninfo->cookie)) {
		fprintf(stderr, _("Failed to set background cookie\n"));
		CloseHandle(evt);
		return 1;
	}

	swprintf(extra_arg, ARRAY_SIZE(extra_arg), L"--background-child=%s", event_name_w);

	argv_w = CommandLineToArgvW(GetCommandLineW(), &argc_w);
	if (!argv_w) {
		char *errstr = openconnect__win32_strerror(GetLastError());
		fprintf(stderr, _("CommandLineToArgv() failed: %s\n"), errstr);
		free(errstr);
		win32_setenv_utf8(OC_BG_COOKIE_ENV, NULL);
		CloseHandle(evt);
		return 1;
	}

	cmdline = build_bg_command_line(argc_w, argv_w, extra_arg);
	LocalFree(argv_w);
	argv_w = NULL;

	if (!cmdline) {
		fprintf(stderr, _("Failed to build background command line\n"));
		win32_setenv_utf8(OC_BG_COOKIE_ENV, NULL);
		CloseHandle(evt);
		return 1;
	}

	memset(&si, 0, sizeof(si));
	si.cb = sizeof(si);
	memset(&pi, 0, sizeof(pi));

	create_flags = DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP;
	ok = CreateProcessW(NULL, cmdline, NULL, NULL, FALSE,
			    create_flags | CREATE_BREAKAWAY_FROM_JOB,
			    NULL, NULL, &si, &pi);
	if (!ok && GetLastError() == ERROR_ACCESS_DENIED) {
		ok = CreateProcessW(NULL, cmdline, NULL, NULL, FALSE,
				    create_flags, NULL, NULL, &si, &pi);
	}

	win32_setenv_utf8(OC_BG_COOKIE_ENV, NULL);
	free(cmdline);

	if (!ok) {
		char *errstr = openconnect__win32_strerror(GetLastError());
		fprintf(stderr, _("CreateProcess() failed: %s\n"), errstr);
		free(errstr);
		CloseHandle(evt);
		return 1;
	}

	CloseHandle(pi.hThread);

	{
		HANDLE handles[2];

		handles[0] = evt;
		handles[1] = pi.hProcess;
		wait_ret = WaitForMultipleObjects(2, handles, FALSE, 0);
	}
	if (wait_ret == WAIT_OBJECT_0) {
		vpn_progress(vpninfo, PRG_INFO,
			     _("Continuing in background; pid %d\n"),
			     (int)pi.dwProcessId);
		exit_code = 0;
	} else if (wait_ret == WAIT_TIMEOUT) {
		vpn_progress(vpninfo, PRG_INFO,
			     _("Background process still starting; continuing anyway (pid %d)\n"),
			     (int)pi.dwProcessId);
		exit_code = 0;
	} else if (wait_ret == WAIT_OBJECT_0 + 1) {
		if (!GetExitCodeProcess(pi.hProcess, &exit_code))
			exit_code = 1;
	} else {
		char *errstr = openconnect__win32_strerror(GetLastError());
		fprintf(stderr, _("WaitForMultipleObjects() failed: %s\n"), errstr);
		free(errstr);
		exit_code = 1;
	}

	CloseHandle(pi.hProcess);
	CloseHandle(evt);
	return (int)exit_code;
}

void win32_background_notify_ready(struct openconnect_info *vpninfo,
				   const char *pidfile, int *wrote_pid)
{
	HANDLE evt;
	wchar_t *event_w;

	if (background_notified || !background_child_event)
		return;

	background_notified = 1;

	if (pidfile && wrote_pid && !*wrote_pid) {
		FILE *fp = openconnect_fopen_utf8(vpninfo, pidfile, "w");
		if (!fp) {
			fprintf(stderr, _("Failed to open '%s' for write: %s\n"),
				pidfile, strerror(errno));
		} else {
			char pidbuf[32];
			int len = snprintf(pidbuf, sizeof(pidbuf), "%lu\n",
					   (unsigned long)GetCurrentProcessId());
			if (len > 0)
				fwrite(pidbuf, 1, len, fp);
			fclose(fp);
			*wrote_pid = 1;
		}
	}

	event_w = utf8_to_wchar(background_child_event);
	if (!event_w)
		return;

	evt = OpenEventW(EVENT_MODIFY_STATE, FALSE, event_w);
	free(event_w);
	if (!evt)
		return;

	SetEvent(evt);
	CloseHandle(evt);
}

#endif /* _WIN32 */
