package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// helpers points the lookup at a directory of this test's making and
// installs the helper names it asks for, so a test can describe the
// system it needs in one line.
func helpers(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("installing %s: %v", name, err)
		}
	}
	original := helperDir
	helperDir = dir
	t.Cleanup(func() { helperDir = original })
	return dir
}

func TestHelperChosenForAFilesystemThatHasOne(t *testing.T) {
	dir := helpers(t, "mount.nfs", "mount.nfs4")
	req := &request{fstype: "nfs", source: "filer:/export", target: "/mnt"}
	if got, want := req.helper(0), filepath.Join(dir, "mount.nfs"); got != want {
		t.Errorf("helper: got %q, want %q", got, want)
	}
}

// The name a helper is called by determines which type it mounts,
// which is how one binary answers as both mount.nfs and mount.nfs4.
func TestHelperFollowsTheTypeName(t *testing.T) {
	dir := helpers(t, "mount.nfs", "mount.nfs4")
	req := &request{fstype: "nfs4", target: "/mnt"}
	if got, want := req.helper(0), filepath.Join(dir, "mount.nfs4"); got != want {
		t.Errorf("helper: got %q, want %q", got, want)
	}
}

// Falling through to the syscall is the ordinary case, not a failure
// path: it is how every filesystem the kernel mounts alone is
// mounted.
func TestNoHelperMeansTheSyscall(t *testing.T) {
	cases := map[string]struct {
		fstype string
		flags  uintptr
	}{
		"a filesystem with no helper installed": {"ext4", 0},
		"no filesystem type at all":             {"", 0},
		"a bind mount":                          {"nfs", unix.MS_BIND},
		"a remount":                             {"nfs", unix.MS_REMOUNT},
		"a move":                                {"nfs", unix.MS_MOVE},
		"a propagation change":                  {"nfs", unix.MS_SHARED | unix.MS_REC},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			helpers(t, "mount.nfs")
			req := &request{fstype: tc.fstype, target: "/mnt"}
			if got := req.helper(tc.flags); got != "" {
				t.Errorf("helper: got %q, want none", got)
			}
		})
	}
}

// A helper that cannot be executed is not a helper. Answering with
// it would turn a mount that the kernel could have performed into a
// failure.
func TestUnexecutableHelperIsIgnored(t *testing.T) {
	dir := helpers(t)
	if err := os.WriteFile(filepath.Join(dir, "mount.nfs"), []byte("not a program"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	req := &request{fstype: "nfs", target: "/mnt"}
	if got := req.helper(0); got != "" {
		t.Errorf("helper: got %q, want none", got)
	}
}
