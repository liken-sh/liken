package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// FS_IOC_GETFLAGS and FS_IOC_SETFLAGS from linux/fs.h, which are
// _IOR('f', 1, long) and _IOW('f', 2, long) on a 64-bit kernel. The
// syscall package does not define them, and the scan program imports
// only the standard library.
const (
	fsIocGetFlags = 0x80086601
	fsIocSetFlags = 0x40086602
)

// osVariableStore returns a variableStore that reads and writes the
// kernel's efivarfs.
func osVariableStore() variableStore {
	return variableStore{flags: inodeFlags, setFlags: setInodeFlags, write: appendOnce}
}

func inodeFlags(path string) (uint32, error) {
	var flags int32
	err := flagsIoctl(path, fsIocGetFlags, &flags)
	return uint32(flags), err
}

// setInodeFlags sets a file's inode flags. A change to the immutable
// flag needs CAP_LINUX_IMMUTABLE.
func setInodeFlags(path string, flags uint32) error {
	value := int32(flags)
	return flagsIoctl(path, fsIocSetFlags, &value)
}

// flagsIoctl sends FS_IOC_GETFLAGS or FS_IOC_SETFLAGS on a file opened
// read-only. The kernel reads and writes the flags as an int, although
// the ioctl numbers encode the size of a long.
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

// appendOnce appends data to the file with a single write(2) call. It
// does not use os.File.Write, which retries after a short write. On
// efivarfs each write(2) is one SetVariable call, so a retry would send
// the rest of the payload as a second write, without the attributes and
// the authentication header that each write must start with.
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
		return fmt.Errorf("the kernel accepted %d of %d bytes", n, len(data))
	}
	return nil
}
