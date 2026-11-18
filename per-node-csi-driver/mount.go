package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// A test process has no privilege to mount, so tests replace
// mountSyscalls with a recorder.
type mountSyscalls interface {
	Mount(source, target, filesystem string, flags uintptr, data string) error
	Unmount(target string, flags int) error
}

type kernelMounts struct{}

func (kernelMounts) Mount(source, target, filesystem string, flags uintptr, data string) error {
	return unix.Mount(source, target, filesystem, flags, data)
}

func (kernelMounts) Unmount(target string, flags int) error {
	return unix.Unmount(target, flags)
}

func bindReadWrite(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind mounting %s onto %s: %w", source, target, err)
	}
	return nil
}

// bindReadOnly mounts twice, because the kernel ignores MS_RDONLY on a
// new bind mount and applies it only on a remount. When the remount
// fails it unmounts the target, so a read-only request never leaves a
// writeable mount.
func bindReadOnly(calls mountSyscalls, source, target string) error {
	if err := calls.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind mounting %s onto %s: %w", source, target, err)
	}
	if err := calls.Mount(source, target, "",
		unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		_ = calls.Unmount(target, unix.MNT_DETACH)
		return fmt.Errorf("remounting %s read-only: %w", target, err)
	}
	return nil
}

func bind(calls mountSyscalls, source, target string, readOnly bool) error {
	if readOnly {
		return bindReadOnly(calls, source, target)
	}
	return bindReadWrite(calls, source, target)
}

// unbind returns no error for a target that is not a mount point
// (EINVAL) or does not exist (ENOENT), so a repeated unpublish
// succeeds.
func unbind(calls mountSyscalls, target string) error {
	err := calls.Unmount(target, unix.MNT_DETACH)
	if err == nil || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return fmt.Errorf("unmounting %s: %w", target, err)
}
