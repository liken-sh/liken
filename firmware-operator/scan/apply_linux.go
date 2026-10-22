package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The ioctls that read and set a file's inode flags, from linux/fs.h,
// as _IOR('f', 1, long) and _IOW('f', 2, long) on a 64-bit kernel. The
// syscall package names neither, and the scan program links nothing
// outside the standard library (scan/Dockerfile).
const (
	fsIocGetFlags = 0x80086601
	fsIocSetFlags = 0x40086602
)

// osVariableStore is the kernel's efivarfs, through the system calls
// that fwupd makes for the same write.
func osVariableStore() variableStore {
	return variableStore{flags: inodeFlags, setFlags: setInodeFlags, write: appendOnce}
}

// inodeFlags reads a file's inode flags with FS_IOC_GETFLAGS.
func inodeFlags(path string) (uint32, error) {
	var flags int32
	err := flagsIoctl(path, fsIocGetFlags, &flags)
	return uint32(flags), err
}

// setInodeFlags sets a file's inode flags with FS_IOC_SETFLAGS. A
// change of the immutable flag needs CAP_LINUX_IMMUTABLE.
func setInodeFlags(path string, flags uint32) error {
	value := int32(flags)
	return flagsIoctl(path, fsIocSetFlags, &value)
}

// flagsIoctl sends one of the two ioctls on a descriptor opened to
// read. The kernel reads and writes an int for the flags, whatever
// size the ioctl's number names, as fwupd and chattr assume too.
func flagsIoctl(path string, request uintptr, flags *int32) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), request, uintptr(unsafe.Pointer(flags))); errno != 0 {
		return errno
	}
	return nil
}

// appendOnce writes data in one write call, through a descriptor opened
// with O_APPEND. It calls write(2) itself, because os.File.Write writes
// again after a short write, and on efivarfs a second write is a second
// SetVariable with the rest of the payload, which no firmware accepts.
func appendOnce(path string, data []byte) error {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer syscall.Close(fd)
	n, err := syscall.Write(fd, data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("the kernel took %d of %d bytes", n, len(data))
	}
	return nil
}
