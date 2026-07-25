package main

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestSplitOptions(t *testing.T) {
	cases := map[string]struct {
		options string
		flags   uintptr
		data    string
	}{
		"nothing":                  {"", 0, ""},
		"a kernel flag":            {"ro", unix.MS_RDONLY, ""},
		"filesystem data":          {"vers=4.1", 0, "vers=4.1"},
		"both kinds mixed":         {"ro,vers=4.1,nosuid", unix.MS_RDONLY | unix.MS_NOSUID, "vers=4.1"},
		"data keeps its order":     {"vers=4.1,rsize=131072", 0, "vers=4.1,rsize=131072"},
		"the last name wins":       {"ro,rw", 0, ""},
		"and in either order":      {"rw,ro", unix.MS_RDONLY, ""},
		"a cleared flag":           {"nosuid,suid", 0, ""},
		"defaults ask for nothing": {"defaults", 0, ""},
		"userspace names drop":     {"_netdev,noauto,nofail", 0, ""},
		"comments drop":            {"x-systemd.requires=network,comment=whatever", 0, ""},
		"empty entries drop":       {"ro,,vers=4", unix.MS_RDONLY, "vers=4"},
		"whitespace is trimmed":    {"ro, vers=4", unix.MS_RDONLY, "vers=4"},

		// The kubelet writes a bind mount this way, and then repeats
		// it with remount to apply the flags, because the kernel
		// ignores everything but MS_BIND on the first call.
		"a bind mount":   {"bind", unix.MS_BIND, ""},
		"a bind remount": {"bind,remount,ro", unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY, ""},

		"recursive propagation": {"rshared", unix.MS_SHARED | unix.MS_REC, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			flags, data := splitOptions(tc.options)
			if flags != tc.flags {
				t.Errorf("flags: got %#x, want %#x", flags, tc.flags)
			}
			if data != tc.data {
				t.Errorf("data: got %q, want %q", data, tc.data)
			}
		})
	}
}

// An option name this program has never seen belongs to the
// filesystem driver, which is the only thing that can judge it. It
// must reach the kernel unchanged rather than fail here.
func TestSplitOptionsPassesUnknownNamesThrough(t *testing.T) {
	flags, data := splitOptions("ro,inventedbysomedriver,alsoinvented=7")
	if flags != unix.MS_RDONLY {
		t.Errorf("flags: got %#x, want %#x", flags, uintptr(unix.MS_RDONLY))
	}
	if want := "inventedbysomedriver,alsoinvented=7"; data != want {
		t.Errorf("data: got %q, want %q", data, want)
	}
}
