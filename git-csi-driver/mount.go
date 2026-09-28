package main

// mount.go holds the bind mount that puts a tree at the kubelet's
// target path.

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// mountSyscalls is the two syscalls the driver makes. It is an interface
// so a test without privilege can watch the calls the driver would make.
// Everything else the driver does to a tree is tested for real.
type mountSyscalls interface {
	Mount(source, target, filesystem string, flags uintptr, data string) error
	Unmount(target string, flags int) error
}

// kernelMounts is the kernel's own syscalls, which the driver uses in
// its pod.
type kernelMounts struct{}

func (kernelMounts) Mount(source, target, filesystem string, flags uintptr, data string) error {
	return unix.Mount(source, target, filesystem, flags, data)
}

func (kernelMounts) Unmount(target string, flags int) error {
	return unix.Unmount(target, flags)
}

// bindReadOnly binds source onto target and makes it read-only. Two
// steps, because the kernel makes the bind first and reads MS_RDONLY
// only on a remount of it. A failed remount unbinds, so a target never
// stays writeable by accident.
func bindReadOnly(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s onto %s: %w", source, target, err)
	}
	if err := calls.Mount(source, target, "",
		unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		_ = calls.Unmount(target, unix.MNT_DETACH)
		return fmt.Errorf("remount %s read-only: %w", target, err)
	}
	return nil
}

// unbind detaches the mount at target. A target that holds no mount is
// not an error, so an unpublish the kubelet repeats answers the same
// way twice.
func unbind(calls mountSyscalls, target string) error {
	err := calls.Unmount(target, unix.MNT_DETACH)
	if err == nil || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return fmt.Errorf("unmount %s: %w", target, err)
}

// bindReadWrite binds source onto target with one call. The pod writes
// the tree, and the driver reads what it wrote.
func bindReadWrite(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s onto %s: %w", source, target, err)
	}
	return nil
}
