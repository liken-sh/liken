package main

// mount.go bind-mounts a work tree at the kubelet's target path.

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// mountSyscalls is an interface so that a test, which runs without the
// privilege to mount, can record the mount calls in place of making
// them.
type mountSyscalls interface {
	Mount(source, target, filesystem string, flags uintptr, data string) error
	Unmount(target string, flags int) error
}

// kernelMounts calls the kernel's mount and umount.
type kernelMounts struct{}

func (kernelMounts) Mount(source, target, filesystem string, flags uintptr, data string) error {
	return unix.Mount(source, target, filesystem, flags, data)
}

func (kernelMounts) Unmount(target string, flags int) error {
	return unix.Unmount(target, flags)
}

// bindReadOnly makes two calls, because the kernel ignores MS_RDONLY on
// the call that creates a bind mount and applies it only on a remount.
// When the remount fails, it unmounts the target, so the pod never gets
// a writeable mount.
func bindReadOnly(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("binding %s onto %s: %w", source, target, err)
	}
	if err := calls.Mount(source, target, "",
		unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		_ = calls.Unmount(target, unix.MNT_DETACH)
		return fmt.Errorf("remounting %s read-only: %w", target, err)
	}
	return nil
}

// unbind detaches the mount at target. A target with no mount (EINVAL)
// or no directory (ENOENT) is not an error, so an unpublish that the
// kubelet repeats succeeds again.
func unbind(calls mountSyscalls, target string) error {
	err := calls.Unmount(target, unix.MNT_DETACH)
	if err == nil || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return fmt.Errorf("unmounting %s: %w", target, err)
}

func bindReadWrite(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("binding %s onto %s: %w", source, target, err)
	}
	return nil
}
