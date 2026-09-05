package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// mountCall is one call the driver made, kept so a test reads its
// arguments.
type mountCall struct {
	source     string
	target     string
	filesystem string
	flags      uintptr
	data       string
}

// recordedMounts records instead of mounting, because a test process is
// not privileged to mount anything.
type recordedMounts struct {
	mounts     []mountCall
	unmounts   []string
	failAt     int
	mountErr   error
	unmountErr error
}

func (r *recordedMounts) Mount(source, target, filesystem string, flags uintptr, data string) error {
	r.mounts = append(r.mounts, mountCall{source, target, filesystem, flags, data})
	if r.mountErr != nil && len(r.mounts) == r.failAt {
		return r.mountErr
	}
	return nil
}

func (r *recordedMounts) Unmount(target string, _ int) error {
	r.unmounts = append(r.unmounts, target)
	return r.unmountErr
}

func TestBindReadOnlyBindsThenRemountsReadOnly(t *testing.T) {
	calls := &recordedMounts{}
	if err := bindReadOnly(calls, "/store/tree", "/kubelet/mount"); err != nil {
		t.Fatalf("bindReadOnly: %v", err)
	}
	want := []mountCall{
		{source: "/store/tree", target: "/kubelet/mount", flags: unix.MS_BIND},
		{
			source: "/store/tree",
			target: "/kubelet/mount",
			flags:  unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY,
		},
	}
	if len(calls.mounts) != len(want) {
		t.Fatalf("bindReadOnly made %v, want %v", calls.mounts, want)
	}
	for i := range want {
		if calls.mounts[i] != want[i] {
			t.Errorf("call %d was %+v, want %+v", i, calls.mounts[i], want[i])
		}
	}
	if len(calls.unmounts) != 0 {
		t.Errorf("bindReadOnly unmounted %v", calls.unmounts)
	}
}

func TestBindReadOnlyReportsABindItCannotMake(t *testing.T) {
	calls := &recordedMounts{failAt: 1, mountErr: unix.EPERM}
	err := bindReadOnly(calls, "/store/tree", "/kubelet/mount")
	if !errors.Is(err, unix.EPERM) {
		t.Fatalf("bindReadOnly answered %v, want %v", err, unix.EPERM)
	}
	if len(calls.mounts) != 1 {
		t.Errorf("bindReadOnly made %v, want the bind alone", calls.mounts)
	}
}

func TestBindReadOnlyUndoesTheBindWhenTheRemountFails(t *testing.T) {
	calls := &recordedMounts{failAt: 2, mountErr: unix.EPERM}
	err := bindReadOnly(calls, "/store/tree", "/kubelet/mount")
	if !errors.Is(err, unix.EPERM) {
		t.Fatalf("bindReadOnly answered %v, want %v", err, unix.EPERM)
	}
	if len(calls.unmounts) != 1 || calls.unmounts[0] != "/kubelet/mount" {
		t.Errorf("bindReadOnly unmounted %v, want the target alone", calls.unmounts)
	}
}

func TestUnbindPassesOverATargetThatHoldsNoMount(t *testing.T) {
	for _, c := range []struct {
		name     string
		failWith error
		wantErr  bool
	}{
		{name: "a mount that comes away", failWith: nil},
		{name: "a target that holds no mount", failWith: unix.EINVAL},
		{name: "a target that is not there", failWith: unix.ENOENT},
		{name: "a target the driver may not unmount", failWith: unix.EPERM, wantErr: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := &recordedMounts{unmountErr: c.failWith}
			err := unbind(calls, "/kubelet/mount")
			if (err != nil) != c.wantErr {
				t.Errorf("unbind answered %v, want an error: %v", err, c.wantErr)
			}
			if len(calls.unmounts) != 1 {
				t.Errorf("unbind unmounted %v, want the target alone", calls.unmounts)
			}
		})
	}
}

func TestTheKernelAnswersTheDriversOwnSyscalls(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "tree")
	target := filepath.Join(dir, "mount")
	for _, made := range []string{source, target} {
		if err := os.MkdirAll(made, 0o755); err != nil {
			t.Fatalf("making %s: %v", made, err)
		}
	}

	err := bindReadOnly(kernelMounts{}, source, target)
	if os.Geteuid() != 0 {
		if err == nil {
			t.Error("an unprivileged bind answered no error")
		}
		// An unprivileged unmount is refused before the kernel looks at the
		// target.
		if got := unbind(kernelMounts{}, target); got == nil {
			t.Error("an unprivileged unmount answered no error")
		}
		return
	}
	if err != nil {
		t.Fatalf("bindReadOnly as root: %v", err)
	}
	if err := unbind(kernelMounts{}, target); err != nil {
		t.Errorf("unbind as root: %v", err)
	}
}
