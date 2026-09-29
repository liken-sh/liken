// Weston's DRM backend subscribes to hotplug events with
// udev_monitor_new_from_netlink(udev, "udev"). Only a running udevd
// broadcasts on that netlink group, and liken runs no udevd, so the
// event that says a monitor arrived never reaches the compositor.
// The kernel broadcasts the same events on the "kernel" group, and
// libudev parses that group's raw format natively.
//
// This library is preloaded into the compositor. It swaps that one
// group name and passes every other call to the real libudev. It
// declares the two libudev types as opaque structs, so it links
// against libc alone and needs no libudev at build time.

#define _GNU_SOURCE
#include <dlfcn.h>
#include <string.h>

struct udev;
struct udev_monitor;

typedef struct udev_monitor *(*new_from_netlink)(struct udev *, const char *);

struct udev_monitor *udev_monitor_new_from_netlink(struct udev *udev, const char *name)
{
	static new_from_netlink real;

	if (real == NULL) {
		real = (new_from_netlink)dlsym(RTLD_NEXT, "udev_monitor_new_from_netlink");
		if (real == NULL) {
			return NULL;
		}
	}
	if (name != NULL && strcmp(name, "udev") == 0) {
		name = "kernel";
	}
	return real(udev, name);
}
