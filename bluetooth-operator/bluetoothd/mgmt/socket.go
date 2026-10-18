package mgmt

import (
	"errors"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

// socket is the kernel's management channel. Each read returns one
// whole packet.
type socket struct {
	fd int
}

// Open opens an HCI socket and binds it to the control channel. The
// kernel's SO_RCVTIMEO ends a read that waits longer than Timeout, so
// a kernel that does not answer delays the caller by that long and no
// more.
func Open() (io.ReadWriteCloser, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return nil, fmt.Errorf("opening an HCI socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: IndexNone, Channel: unix.HCI_CHANNEL_CONTROL}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("binding the HCI socket to the management channel: %w", err)
	}
	timeout := unix.NsecToTimeval(Timeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("setting a read timeout on the management channel: %w", err)
	}
	return &socket{fd: fd}, nil
}

func (s *socket) Read(buffer []byte) (int, error) {
	n, err := unix.Read(s.fd, buffer)
	if errors.Is(err, unix.EAGAIN) {
		return 0, fmt.Errorf("the kernel sent no reply within %s", Timeout)
	}
	if err != nil {
		return 0, fmt.Errorf("reading the management channel: %w", err)
	}
	return n, nil
}

func (s *socket) Write(packet []byte) (int, error) {
	n, err := unix.Write(s.fd, packet)
	if err != nil {
		return 0, fmt.Errorf("writing to the management channel: %w", err)
	}
	return n, nil
}

func (s *socket) Close() error {
	return unix.Close(s.fd)
}
