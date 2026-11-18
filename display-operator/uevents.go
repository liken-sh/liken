package main

// The kernel broadcasts every device change on a netlink socket of
// protocol NETLINK_KOBJECT_UEVENT. Each datagram is "action@devpath"
// followed by KEY=VALUE pairs, and each part ends with a NUL byte.
// Plugging in or unplugging a monitor produces a "change" event on the
// drm subsystem with HOTPLUG=1. The event does not hold the new state of
// the connector, so the operator reads sysfs again when one arrives.
//
// The socket opens without an error and then receives nothing in two
// cases, so avoid both:
//
//   - A bind to group 2. Group 1 is the kernel's own broadcasts, and
//     group 2 is what udev sends again to libudev clients. A machine
//     with no udev sends nothing to group 2.
//   - A process outside the initial user namespace. The kernel sends
//     uevents only to that namespace. An unprivileged process in it can
//     receive group 1, because the kernel creates the uevent socket with
//     NL_CFG_F_NONROOT_RECV.

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// drmEvent is a change on the drm subsystem, or a report that the
// socket lost datagrams. For a loss, Action is lostEventsAction and
// DevPath holds the socket error. hotplugCounts uses DevPath and
// Connector to count the events of each connector of one card.
type drmEvent struct {
	Action  string
	DevPath string
	// Connector is the DRM object id from the event's CONNECTOR
	// property. It is empty when the event does not name a connector.
	Connector string
}

// parseUevent returns false for a datagram that does not start with
// "action@devpath", such as a libudev message.
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

// drmEventFrom keeps "add" and "remove" as well as "change", because
// they mean that a card arrived or left, and the operator reads sysfs
// again for those too. A hotplug is a "change".
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
		return drmEvent{Action: action, DevPath: devpath, Connector: values["CONNECTOR"]}, true
	}
	return drmEvent{}, false
}

// listenForUevents returns a channel that is closed when ctx ends.
// The reader waits in poll on the socket and on a cancel pipe, because
// closing a socket descriptor does not wake a thread that is blocked
// reading it. Closing the pipe's write end does.
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
			// A lost datagram may have been a hotplug, so the loss wakes
			// a pass now, not at the next event or the backstop tick.
			if event, lost := lostUevents(err); lost {
				sendDRMEvent(events, event)
			}
			continue
		}
		event, ok := drmEventFrom(buf[:size])
		if !ok {
			continue
		}
		sendDRMEvent(events, event)
	}
}

const lostEventsAction = "lost events"

// lostUevents reports whether a receive error may have dropped
// datagrams. ENOBUFS means that the receive buffer overflowed and the
// kernel dropped datagrams. EAGAIN and EINTR drop nothing. Any other
// error counts as a loss, because poll reported the socket readable and
// the read returned no datagram.
func lostUevents(err error) (drmEvent, bool) {
	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return drmEvent{}, false
	}
	return drmEvent{Action: lostEventsAction, DevPath: "reading the uevent socket: " + err.Error()}, true
}

// sendDRMEvent drops the event when the channel is full, because the
// events already queued wake a pass that reads all of sysfs.
func sendDRMEvent(events chan<- drmEvent, event drmEvent) {
	select {
	case events <- event:
	default:
	}
}
