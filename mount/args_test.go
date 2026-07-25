package main

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// These are the command lines the kubelet builds, which are the only
// ones this program ever sees in service.
func TestParseArgsReadsKubeletCommandLines(t *testing.T) {
	cases := map[string]struct {
		argv    []string
		source  string
		target  string
		fstype  string
		options string
		flags   uintptr
	}{
		"an inline nfs volume": {
			argv:    []string{"-t", "nfs", "-o", "ro", "filer:/export", "/var/lib/kubelet/pods/x/volumes/nfs/data"},
			source:  "filer:/export",
			target:  "/var/lib/kubelet/pods/x/volumes/nfs/data",
			fstype:  "nfs",
			options: "ro",
		},
		"a memory emptyDir": {
			argv:   []string{"-t", "tmpfs", "-o", "size=64Mi", "tmpfs", "/var/lib/kubelet/pods/x/volumes/empty"},
			source: "tmpfs", target: "/var/lib/kubelet/pods/x/volumes/empty",
			fstype: "tmpfs", options: "size=64Mi",
		},
		"a subpath bind": {
			argv:   []string{"-o", "bind", "/data/claim", "/data/claim/sub"},
			source: "/data/claim", target: "/data/claim/sub", options: "bind",
		},
		"the remount that follows it": {
			argv:   []string{"-o", "bind,remount,ro", "/data/claim", "/data/claim/sub"},
			source: "/data/claim", target: "/data/claim/sub", options: "bind,remount,ro",
		},
		"a propagation change": {
			argv:   []string{"--make-rshared", "/"},
			target: "/", flags: unix.MS_SHARED | unix.MS_REC,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req, err := parseArgs(tc.argv)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if req.source != tc.source {
				t.Errorf("source: got %q, want %q", req.source, tc.source)
			}
			if req.target != tc.target {
				t.Errorf("target: got %q, want %q", req.target, tc.target)
			}
			if req.fstype != tc.fstype {
				t.Errorf("fstype: got %q, want %q", req.fstype, tc.fstype)
			}
			if req.optionString() != tc.options {
				t.Errorf("options: got %q, want %q", req.optionString(), tc.options)
			}
			if req.flags != tc.flags {
				t.Errorf("flags: got %#x, want %#x", req.flags, tc.flags)
			}
		})
	}
}

func TestParseArgsAcceptsTheOlderSpellings(t *testing.T) {
	cases := map[string]struct {
		argv    []string
		fstype  string
		options string
		flags   uintptr
	}{
		"a value attached to its name": {argv: []string{"-tnfs4", "-oro", "a", "b"}, fstype: "nfs4", options: "ro"},
		"clustered short names":        {argv: []string{"-rv", "a", "b"}, options: "ro"},
		"a repeated option list":       {argv: []string{"-o", "ro", "-o", "vers=4.1", "a", "b"}, options: "ro,vers=4.1"},
		"long names":                   {argv: []string{"--types", "nfs", "--options", "ro", "a", "b"}, fstype: "nfs", options: "ro"},
		"read-only as a flag":          {argv: []string{"--read-only", "a", "b"}, options: "ro"},
		"bind as a long name":          {argv: []string{"--bind", "a", "b"}, flags: unix.MS_BIND},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req, err := parseArgs(tc.argv)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if req.fstype != tc.fstype {
				t.Errorf("fstype: got %q, want %q", req.fstype, tc.fstype)
			}
			if req.optionString() != tc.options {
				t.Errorf("options: got %q, want %q", req.optionString(), tc.options)
			}
			if req.flags != tc.flags {
				t.Errorf("flags: got %#x, want %#x", req.flags, tc.flags)
			}
		})
	}
}

func TestParseArgsKeepsPathsAfterADoubleDash(t *testing.T) {
	req, err := parseArgs([]string{"-t", "ext4", "--", "-weird", "/mnt"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if req.source != "-weird" || req.target != "/mnt" {
		t.Errorf("paths: got %q and %q, want %q and %q", req.source, req.target, "-weird", "/mnt")
	}
}

func TestParseArgsRefusesCommandLinesItCannotAct(t *testing.T) {
	cases := map[string]struct {
		argv []string
		says string
	}{
		"everything in fstab":   {[]string{"-a"}, "fstab"},
		"no paths at all":       {[]string{"-t", "nfs"}, "no mount point"},
		"a missing value":       {[]string{"-t"}, "needs a value"},
		"an unknown long name":  {[]string{"--invented", "a", "b"}, "unknown option"},
		"an unknown short name": {[]string{"-Z", "a", "b"}, "unknown option"},
		"three paths":           {[]string{"a", "b", "c"}, "too many paths"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseArgs(tc.argv)
			if err == nil {
				t.Fatalf("parsing %v: no error", tc.argv)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error %q does not mention %q", err, tc.says)
			}
		})
	}
}

// A helper takes the options exactly as they arrived, because the
// helper is what decides which of them the kernel ever sees.
func TestHelperArgsFollowTheHelperContract(t *testing.T) {
	req, err := parseArgs([]string{"-t", "nfs", "-o", "ro,vers=4.1", "-v", "filer:/export", "/mnt"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	got := req.helperArgs("/sbin/mount.nfs")
	want := []string{"/sbin/mount.nfs", "filer:/export", "/mnt", "-v", "-o", "ro,vers=4.1"}
	if !slices.Equal(got, want) {
		t.Errorf("helper command line:\n got %q\nwant %q", got, want)
	}
}

func TestHelperArgsOmitAnEmptyOptionList(t *testing.T) {
	req, err := parseArgs([]string{"-t", "nfs", "filer:/export", "/mnt"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	got := req.helperArgs("/sbin/mount.nfs")
	want := []string{"/sbin/mount.nfs", "filer:/export", "/mnt"}
	if !slices.Equal(got, want) {
		t.Errorf("helper command line:\n got %q\nwant %q", got, want)
	}
}
