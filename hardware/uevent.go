package hardware

// Uevents are how the kernel reports that hardware changed.
//
// The kernel broadcasts every device add, remove, and driver bind on
// a netlink socket (NETLINK_KOBJECT_UEVENT, multicast group 1). This
// is the same channel that udev listens on, where udev exists. Each
// datagram is "action@devpath" followed by KEY=VALUE pairs,
// including the MODALIAS fingerprint, but this listener deliberately
// reads none of that detail. A uevent only signals that something
// changed. The sysfs walk re-reads the whole state moments later.
// This is simpler and more accurate than incrementally mirroring
// kernel state from event payloads, because a mirror can drift out
// of sync, while a re-walk cannot.

import (
	"bytes"
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

// ListenForUevents opens the kernel's uevent socket and returns a
// channel. This channel signals whenever a device appears,
// disappears, or changes drivers. The channel holds one pending
// signal and drops the rest. For example, a burst of eleven uevents
// from one USB stick's enumeration needs one re-walk, not eleven.
func ListenForUevents(ctx context.Context) (<-chan struct{}, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("opening the uevent socket: %w", err)
	}
	// Group 1 carries the kernel's own broadcasts. Group 2 carries
	// udev's re-broadcasts to libudev clients, but on a liken
	// machine, nothing sends to group 2.
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
			// This code polls with a timeout instead of blocking
			// forever in a read call, because a blocked read has no
			// way to notice that the context ended.
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

// hardwareChanged decides whether one uevent datagram requires a
// re-walk. Add and remove events change what exists. Bind and unbind
// events change which driver is bound. Every other event, such as
// change, move, or the online and offline events for memory blocks,
// changes nothing that this package reports.
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
