#ifndef OPENCONNECT_WIN32_BACKGROUND_H
#define OPENCONNECT_WIN32_BACKGROUND_H

#ifdef _WIN32

struct openconnect_info;

int win32_background_is_child(void);
void win32_background_set_child_event(char *event_name);
void win32_background_apply_cookie(struct openconnect_info *vpninfo);
int win32_spawn_background_child(int argc, char **argv, struct openconnect_info *vpninfo);
void win32_background_notify_ready(struct openconnect_info *vpninfo,
				   const char *pidfile, int *wrote_pid);
int win32_unlink_utf8(const char *path);

#endif /* _WIN32 */

#endif /* OPENCONNECT_WIN32_BACKGROUND_H */
