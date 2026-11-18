package main

// golang.org/x/sys/unix defines none of the input subsystem's request
// numbers, so evdev.go and uinput.go build theirs with ioc. A test pins
// them against the values in linux/input.h and linux/uinput.h.

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// The fields of an ioctl request number, as the kernel's _IOC macro
// packs them: the direction of the transfer, the size of the argument,
// a letter that identifies the driver, and a number within that driver.
const (
	iocNone  = 0
	iocWrite = 1
	iocRead  = 2

	iocTypeShift      = 8
	iocSizeShift      = 16
	iocDirectionShift = 30

	evdevLetter  = 'E'
	uinputLetter = 'U'
)

func ioc(direction, size, letter, number uint32) uint32 {
	return direction<<iocDirectionShift | size<<iocSizeShift | letter<<iocTypeShift | number
}

// ioctlPtr calls an ioctl whose argument is a pointer to a struct or a
// buffer.
func ioctlPtr(fd int, request uint32, argument unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(argument)); errno != 0 {
		return errno
	}
	return nil
}

// ioctlInt calls an ioctl whose argument is an integer passed by value.
// The UI_SET_*BIT requests take their bit number this way.
func ioctlInt(fd int, request uint32, value int) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(value)); errno != 0 {
		return errno
	}
	return nil
}
