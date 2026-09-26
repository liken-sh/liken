package cec

// The real handle: a file descriptor on /dev/cecN.

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fileHandle is one open adapter node.
type fileHandle struct {
	fd int
}

// Open opens an adapter node, such as /dev/cec0. The handle is
// blocking, so a transmit returns after the message is on the wire and
// a claim returns after the kernel claimed the address. Receive and
// Wait take their own timeouts, so no read blocks forever.
func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return New(&fileHandle{fd: fd}), nil
}

func (h *fileHandle) Ioctl(request uintptr, argument unsafe.Pointer) error {
	// The pointer converts to a uintptr inside the call expression,
	// which is the one form the Go runtime keeps the pointed-to memory
	// alive and in place for.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(h.fd), request, uintptr(argument))
	if errno != 0 {
		return errno
	}
	return nil
}

// Wait polls the node. The CEC core signals a queued message as
// readable and a queued event as priority data. A node whose adapter
// left reports an error and a hang-up, and Wait turns that into
// ENODEV, the error every other call on the handle returns.
func (h *fileHandle) Wait(timeout time.Duration) (Readiness, error) {
	fds := []unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN | unix.POLLPRI}}
	for {
		_, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return Readiness{}, fmt.Errorf("poll: %w", err)
		}
		break
	}
	events := fds[0].Revents
	if events&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return Readiness{}, fmt.Errorf("poll: %w", unix.ENODEV)
	}
	return Readiness{Message: events&unix.POLLIN != 0, Event: events&unix.POLLPRI != 0}, nil
}

func (h *fileHandle) Close() error {
	return unix.Close(h.fd)
}
