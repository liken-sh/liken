package main

// Listening for the kernel's display hotplug events.
//
// The kernel broadcasts every device change on a netlink socket,
// NETLINK_KOBJECT_UEVENT. Each datagram is "action@devpath" followed
// by KEY=VALUE pairs, each part ending in a NUL byte. A monitor
// plugged in or unplugged produces a "change" event on the drm
// subsystem, with HOTPLUG=1, for the card rather than for the
// connector, so the event says only that something moved and the
// operator re-reads sysfs for the new state.
//
// Two ways to open this socket fail silently:
//
//   - Bind group 1, never group 2. Group 1 carries the kernel's own
//     broadcasts. Group 2 carries udev's re-broadcasts to libudev
//     clients. On a machine with no udev the bind to group 2
//     succeeds, the socket opens, and it delivers nothing forever,
//     because nothing writes to that group.
//   - Do not run in a non-init user namespace. The kernel delivers
//     uevents to the initial user namespace only, and a process in its
//     own user namespace receives an empty stream with no error to
//     read. An unprivileged process in the initial namespace may
//     receive group 1, because the kernel creates the uevent socket
//     with NL_CFG_F_NONROOT_RECV.

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// drmEvent reports that something changed on the drm subsystem.
// Action is the kernel's word, and DevPath names the card or the
// connector the kernel attached the event to. Neither holds the new
// state, so both exist for the log line alone.
type drmEvent struct {
	Action  string
	DevPath string
}

// parseUevent splits one datagram into its action, its DEVPATH, and
// its key-value pairs. A datagram that does not start with
// "action@devpath" is a libudev message on the same socket, and the
// second return value reports that.
func parseUevent(datagram []byte) (action, devpath string, values map[string]string, ok bool) {
	parts := bytes.Split(bytes.TrimRight(datagram, "\x00"), []byte{0})
	if len(parts) == 0 {
		return "", "", nil, false
	}
	head, path, found := bytes.Cut(parts[0], []byte("@"))
	if !found {
		return "", "", nil, false
	}
	values = map[string]string{}
	for _, part := range parts[1:] {
		key, value, found := bytes.Cut(part, []byte("="))
		if found {
			values[string(key)] = string(value)
		}
	}
	return string(head), string(path), values, true
}

// drmEventFrom turns one datagram into an event, when the datagram
// reports a change on the drm subsystem. Everything else on the
// socket, which on a running machine is most of it, reports false.
//
// The subsystem test drops every other subsystem's events, and the
// action test keeps a monitor's own events in the stream. A hotplug is a
// "change" on an existing card, not an "add": the card and its
// connectors were registered when the driver bound. An "add" or a
// "remove" of a drm device is the card itself arriving or leaving, and
// the operator re-reads sysfs for those too.
func drmEventFrom(datagram []byte) (drmEvent, bool) {
	action, devpath, values, ok := parseUevent(datagram)
	if !ok {
		return drmEvent{}, false
	}
	if values["SUBSYSTEM"] != "drm" {
		return drmEvent{}, false
	}
	switch action {
	case "add", "change", "remove":
		return drmEvent{Action: action, DevPath: devpath}, true
	}
	return drmEvent{}, false
}

// listenForUevents opens the kernel's uevent socket and returns a
// channel of drm events. The channel is buffered, and a full channel
// drops the event: every consumer of this channel re-reads the whole
// of sysfs, so the events say when to look, never what is there.
//
// The socket is non-blocking. The reader waits for it in poll, not in
// a read, so it can also watch a cancel pipe in the same poll and stop
// the moment the context ends. This is liken's own arrangement, for
// the reason liken states: closing a descriptor does not wake a thread
// already blocked on a read of it.
func listenForUevents(ctx context.Context) (<-chan drmEvent, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("opening the uevent socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: 1}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("binding the uevent socket: %w", err)
	}
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("opening the cancel pipe: %w", err)
	}
	events := make(chan drmEvent, 16)
	go func() {
		<-ctx.Done()
		unix.Close(pipe[1])
	}()
	go readUevents(fd, pipe[0], events)
	return events, nil
}

// readUevents is the reader loop. It blocks in poll over the uevent
// socket and the cancel pipe. A ready socket means a datagram to read;
// a ready cancel pipe means the context is done and the loop returns.
// It closes the descriptors it owns as it leaves.
func readUevents(fd, cancelRead int, events chan<- drmEvent) {
	defer unix.Close(fd)
	defer unix.Close(cancelRead)
	defer close(events)

	buf := make([]byte, 64<<10)
	fds := []unix.PollFd{
		{Fd: int32(fd), Events: unix.POLLIN},
		{Fd: int32(cancelRead), Events: unix.POLLIN},
	}
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		size, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			// EAGAIN means the poll woke with no datagram to read. Any
			// other error left this datagram unread. A missed datagram
			// costs one late reconcile at worst, because the backstop
			// tick in main.go re-reads sysfs anyway.
			continue
		}
		event, ok := drmEventFrom(buf[:size])
		if !ok {
			continue
		}
		select {
		case events <- event:
		default:
		}
	}
}
