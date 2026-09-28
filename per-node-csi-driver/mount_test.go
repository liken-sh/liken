package main

import (
	"errors"
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

func TestBindReadWriteBindsTheCopyOnceOntoTheTarget(t *testing.T) {
	calls := &recordedMounts{}
	if err := bind(calls, "/store/copies/example-store", "/kubelet/mount", false); err != nil {
		t.Fatalf("bind: %v", err)
	}
	want := []mountCall{
		{source: "/store/copies/example-store", target: "/kubelet/mount", flags: unix.MS_BIND},
	}
	if len(calls.mounts) != len(want) || calls.mounts[0] != want[0] {
		t.Fatalf("bind made %v, want %v", calls.mounts, want)
	}
}

func TestBindReadOnlyBindsThenRemountsReadOnly(t *testing.T) {
	calls := &recordedMounts{}
	if err := bind(calls, "/store/copies/example-store", "/kubelet/mount", true); err != nil {
		t.Fatalf("bind: %v", err)
	}
	want := []mountCall{
		{source: "/store/copies/example-store", target: "/kubelet/mount", flags: unix.MS_BIND},
		{
			source: "/store/copies/example-store",
			target: "/kubelet/mount",
			flags:  unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY,
		},
	}
	if len(calls.mounts) != len(want) || calls.mounts[0] != want[0] || calls.mounts[1] != want[1] {
		t.Fatalf("bind made %v, want %v", calls.mounts, want)
	}
}

func TestABindThatFailsReportsTheSourceAndTheTarget(t *testing.T) {
	calls := &recordedMounts{failAt: 1, mountErr: unix.EPERM}
	err := bind(calls, "/store/copies/example-store", "/kubelet/mount", false)
	if err == nil {
		t.Fatal("bind answered no error, want one")
	}
	if !errors.Is(err, unix.EPERM) {
		t.Errorf("bind answered %v, want it to carry EPERM", err)
	}
}

func TestAReadOnlyRemountThatFailsUnbindsTheTarget(t *testing.T) {
	calls := &recordedMounts{failAt: 2, mountErr: unix.EPERM}
	err := bind(calls, "/store/copies/example-store", "/kubelet/mount", true)
	if err == nil {
		t.Fatal("bind answered no error, want one")
	}
	if len(calls.unmounts) != 1 || calls.unmounts[0] != "/kubelet/mount" {
		t.Errorf("bind unmounted %v, want /kubelet/mount", calls.unmounts)
	}
}

func TestAReadOnlyBindThatFailsBeforeTheRemountReportsIt(t *testing.T) {
	calls := &recordedMounts{failAt: 1, mountErr: unix.EPERM}
	if err := bind(calls, "/store/copies/example-store", "/kubelet/mount", true); err == nil {
		t.Fatal("bind answered no error, want one")
	}
}

func TestUnbindTakesATargetThatHoldsNoMountAsDone(t *testing.T) {
	for _, c := range []struct {
		name      string
		answer    error
		reported  bool
		unmounted string
	}{
		{name: "the mount came away", unmounted: "/kubelet/mount"},
		{name: "the target holds no mount", answer: unix.EINVAL, unmounted: "/kubelet/mount"},
		{name: "the target is gone", answer: unix.ENOENT, unmounted: "/kubelet/mount"},
		{name: "the kernel refused", answer: unix.EPERM, reported: true, unmounted: "/kubelet/mount"},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := &recordedMounts{unmountErr: c.answer}
			err := unbind(calls, "/kubelet/mount")
			if c.reported != (err != nil) {
				t.Fatalf("unbind answered %v, want an error: %v", err, c.reported)
			}
			if len(calls.unmounts) != 1 || calls.unmounts[0] != c.unmounted {
				t.Errorf("unbind unmounted %v, want %s", calls.unmounts, c.unmounted)
			}
		})
	}
}

func TestTheKernelsOwnSyscallsRefuseAnUnprivilegedProcess(t *testing.T) {
	calls := kernelMounts{}
	if err := calls.Mount(t.TempDir(), t.TempDir(), "", unix.MS_BIND, ""); err == nil {
		t.Error("Mount answered no error, want one from an unprivileged process")
	}
	if err := calls.Unmount(t.TempDir(), unix.MNT_DETACH); err == nil {
		t.Error("Unmount answered no error, want one from an unprivileged process")
	}
}
