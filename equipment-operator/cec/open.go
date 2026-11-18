package cec

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fileHandle talks to a real adapter through its /dev/cecN node.
type fileHandle struct {
	fd int
}

// Open opens an adapter node, such as /dev/cec0. It opens the node in
// blocking mode, so a transmit returns after the message is on the bus
// and a claim returns after the kernel has claimed the address.
func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return New(&fileHandle{fd: fd}), nil
}

func (h *fileHandle) Ioctl(request uintptr, argument unsafe.Pointer) error {
	// The conversion to uintptr must stay inside the Syscall call
	// expression, because only then does the Go runtime keep the memory
	// alive and in place during the call.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(h.fd), request, uintptr(argument))
	if errno != 0 {
		return errno
	}
	return nil
}

// Wait polls the node for up to timeout. The kernel's CEC core reports
// a queued message as POLLIN and a queued event as POLLPRI. When the
// adapter is removed, poll reports POLLERR or POLLHUP, and Wait returns
// ENODEV, the error that every other call on a removed adapter returns.
func (h *fileHandle) Wait(timeout time.Duration) (Readiness, error) {
	fds := []unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN | unix.POLLPRI}}
	for {
		_, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return Readiness{}, fmt.Errorf("polling the node: %w", err)
		}
		break
	}
	events := fds[0].Revents
	if events&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return Readiness{}, fmt.Errorf("polling the node: %w", unix.ENODEV)
	}
	return Readiness{Message: events&unix.POLLIN != 0, Event: events&unix.POLLPRI != 0}, nil
}

func (h *fileHandle) Close() error {
	return unix.Close(h.fd)
}
