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
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ListenForUevents opens the kernel's uevent socket and returns a
// channel. This channel signals whenever a device appears,
// disappears, or changes drivers. The channel holds one pending
// signal and drops the rest. For example, a burst of eleven uevents
// from one USB stick's enumeration needs one re-walk, not eleven.
//
// The socket is non-blocking. The reader waits for it in poll, not in
// a read, so it can also watch a cancel pipe in the same poll and stop
// the moment the context ends. See watchUevents and readUevents for the
// wake and the stop.
func ListenForUevents(ctx context.Context) (<-chan struct{}, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_KOBJECT_UEVENT)
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
	notify, err := watchUevents(ctx, fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return notify, nil
}

// watchUevents starts the reader over the non-blocking socket fd and
// returns its wake channel. It owns fd from this point: the reader
// closes it on exit.
//
// Cancellation cannot rely on closing fd, because a close does not wake
// a thread already blocked in a read on that descriptor. So the reader
// never blocks in a read. It waits in poll over fd and the read end of
// a cancel pipe. When the context is done, a second goroutine closes
// the pipe's write end. That close puts a hangup on the read end, the
// poll wakes, and the reader returns. This split of ownership closes
// every descriptor once: the cancel goroutine closes the write end, and
// the reader closes fd and the read end as it leaves.
func watchUevents(ctx context.Context, fd int) (<-chan struct{}, error) {
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		return nil, fmt.Errorf("opening the cancel pipe: %w", err)
	}
	notify := make(chan struct{}, 1)
	go func() {
		<-ctx.Done()
		unix.Close(pipe[1])
	}()
	go readUevents(fd, pipe[0], notify)
	return notify, nil
}

// readUevents is the reader loop. It blocks in poll over the uevent
// socket and the cancel pipe. A ready socket means a datagram to read;
// a ready cancel pipe means the context is done and the loop returns. It
// closes the descriptors it owns as it leaves.
func readUevents(fd, cancelR int, notify chan<- struct{}) {
	defer unix.Close(fd)
	defer unix.Close(cancelR)
	buf := make([]byte, 64<<10)
	fds := []unix.PollFd{
		{Fd: int32(fd), Events: unix.POLLIN},
		{Fd: int32(cancelR), Events: unix.POLLIN},
	}
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			// A signal interrupted the wait. Wait again.
			continue
		}
		if err != nil {
			return
		}
		if fds[1].Revents != 0 {
			// The cancel pipe reports a hangup. The context is done.
			return
		}
		size, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if recvErrorLostAUevent(err) {
				// ENOBUFS means the kernel's receive buffer overflowed and
				// it dropped datagrams before this call ever ran; any other
				// error here is one this reader has no known cause for on
				// this socket, and either way poll reported the socket
				// ready and this call still returned no datagram. Hardware
				// may have changed with the loss, so wake the sysfs walk
				// now rather than wait for an unrelated later uevent to
				// trigger it by accident.
				wake(notify)
			}
			// EAGAIN means the poll woke without a datagram to read, and
			// EINTR means a signal interrupted the read before it took
			// anything. Neither one loses a datagram, so wait for the
			// next event with no wake.
			continue
		}
		if !hardwareChanged(buf[:size]) {
			continue
		}
		wake(notify)
	}
}

// recvErrorLostAUevent reports whether a Recvfrom error on the uevent
// socket left a datagram unread. EAGAIN means poll woke with nothing
// queued, and EINTR means a signal interrupted the call before it read
// anything; neither one loses a datagram. Every other error does, most
// often ENOBUFS: poll reported the socket ready, and the call still did
// not return a datagram.
func recvErrorLostAUevent(err error) bool {
	return !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR)
}

// wake does one non-blocking send on the notify channel. The channel
// holds one pending wake, so a send while one is already queued is
// dropped: the walk that drains the channel reads the whole state, so
// coalescing loses nothing.
func wake(notify chan<- struct{}) {
	select {
	case notify <- struct{}{}:
	default:
	}
}

// hardwareChanged reports whether one uevent datagram requires a
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
