package hardware

// Uevents: how the kernel announces that the hardware changed.
//
// Every device add, remove, and driver bind is broadcast on a
// netlink socket (NETLINK_KOBJECT_UEVENT, multicast group 1), the
// same channel udev listens on where udev exists. Each datagram is
// "action@devpath" followed by KEY=VALUE pairs — including the
// MODALIAS fingerprint — but this listener deliberately reads none
// of the detail. A uevent is only a doorbell: the sysfs walk
// re-reads the whole truth moments later, which is simpler and
// more honest than incrementally mirroring kernel state from event
// payloads (a mirror can drift; a re-walk cannot).

import (
	"bytes"
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

// ListenForUevents opens the kernel's uevent socket and returns a
// channel that signals whenever a device appeared, disappeared, or
// changed drivers. The channel holds one pending signal and drops
// the rest: a burst of eleven uevents (one USB stick's enumeration
// cascade) needs one re-walk, not eleven.
func ListenForUevents(ctx context.Context) (<-chan struct{}, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("opening the uevent socket: %w", err)
	}
	// Group 1 is the kernel's own broadcasts. (Group 2 carries
	// udev's re-broadcasts to libudev clients; on a liken machine
	// nobody sends there.)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: 1}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("binding the uevent socket: %w", err)
	}

	notify := make(chan struct{}, 1)
	go func() {
		defer unix.Close(fd)
		buf := make([]byte, 64<<10)
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for {
			// Poll with a timeout rather than block forever in read:
			// a blocked read has no way to notice the context ending.
			n, err := unix.Poll(fds, 1000)
			if ctx.Err() != nil {
				return
			}
			if err != nil || n == 0 {
				continue
			}
			size, _, err := unix.Recvfrom(fd, buf, 0)
			if err != nil || !hardwareChanged(buf[:size]) {
				continue
			}
			select {
			case notify <- struct{}{}:
			default:
			}
		}
	}()
	return notify, nil
}

// hardwareChanged decides whether one uevent datagram is worth a
// re-walk. Add and remove change what exists; bind and unbind change
// what is driven; everything else (change, move, the online/offline
// of memory blocks) changes nothing this package reports.
func hardwareChanged(datagram []byte) bool {
	action, _, found := bytes.Cut(datagram, []byte("@"))
	if !found {
		return false
	}
	switch string(action) {
	case "add", "remove", "bind", "unbind":
		return true
	}
	return false
}
