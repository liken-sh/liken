package main

// How this program calls an ioctl. The kernel builds a request number
// out of the direction of the transfer, the size of the argument, a
// letter that names the driver, and a number within that letter.
// golang.org/x/sys/unix carries none of the input layer's request
// numbers, so evdev.go and uinput.go build theirs here, and a test pins
// them against the values linux/input.h and linux/uinput.h define.

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// The pieces of the kernel's _IOC macro: the direction of the transfer,
// then the size of the argument, the letter that names the driver, and
// the number within that letter.
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

// ioc builds a request number the way the kernel's _IOC macro does.
func ioc(direction, size, letter, number uint32) uint32 {
	return direction<<iocDirectionShift | size<<iocSizeShift | letter<<iocTypeShift | number
}

// ioctlPtr makes an ioctl whose argument is a pointer to a structure
// or a buffer.
func ioctlPtr(fd int, request uint32, argument unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(argument)); errno != 0 {
		return errno
	}
	return nil
}

// ioctlInt makes an ioctl whose argument is the value itself. Every
// UI_SET_*BIT call is one of these: the request number declares an int
// and the kernel reads the bit number out of the argument register.
func ioctlInt(fd int, request uint32, value int) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(value)); errno != 0 {
		return errno
	}
	return nil
}
