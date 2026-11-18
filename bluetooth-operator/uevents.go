package main

// The kernel broadcasts each device add, remove, and change on a
// NETLINK_KOBJECT_UEVENT socket. This file wakes the loop for these:
//
//   - A HID add or remove: a controller connected or disconnected. The
//     add arrives before bluetoothd reports the connection on D-Bus.
//   - An input add of an evdev node under a controller. The operator
//     opens the node on this event without waiting for settleWindow,
//     because the kernel discards a node's events until a reader opens
//     it.
//   - A power supply change: a controller's battery reported a new
//     level or charging status.
//
// Two mistakes in opening the socket fail with no error:
//
//   - Binding multicast group 2 instead of group 1. Group 1 receives
//     the kernel's broadcasts, and group 2 receives what udev sends
//     again to libudev clients. On a machine with no udev, nothing
//     sends to group 2.
//   - Running in a user namespace other than the initial one. The
//     kernel sends uevents only to the initial user namespace, so such
//     a process receives nothing. The process does not need root,
//     because the kernel creates the uevent socket with
//     NL_CFG_F_NONROOT_RECV.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// kernelEvent is one uevent the loop acts on, or, when Lost is true, a
// report that datagrams were lost. MAC is set for a HID or input event
// and is empty for a power supply change, whose datagram has no
// address.
type kernelEvent struct {
	Subsystem string
	Action    string
	MAC       string
	Lost      bool
}

const (
	subsystemHID         = "hid"
	subsystemInput       = "input"
	subsystemPowerSupply = "power_supply"
)

// devpathMACs maps the DEVPATH of each present HID device to its
// controller's MAC address. A remove datagram can lack HID_UNIQ, and
// the device's sysfs directory is already gone, so the map is the only
// record of which controller left.
type devpathMACs struct {
	mutex sync.Mutex
	macs  map[string]string
}

func newDevpathMACs() *devpathMACs {
	return &devpathMACs{macs: map[string]string{}}
}

// resolve returns the MAC address of a HID event's controller: uniq
// when it is a valid address, otherwise the address recorded for
// devpath. It returns an empty string when neither is valid, as for a
// HID device that is not a Bluetooth peripheral. An add records the
// address and a remove deletes it.
func (d *devpathMACs) resolve(action, devpath, uniq string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	mac := normalizeMAC(uniq)
	if !validMAC(mac) {
		mac = d.macs[devpath]
	}
	switch action {
	case "add":
		if validMAC(mac) {
			d.macs[devpath] = mac
		}
	case "remove":
		delete(d.macs, devpath)
	}
	if !validMAC(mac) {
		return ""
	}
	return mac
}

// macOfParent returns the MAC address recorded for the HID device that
// devpath is under, or an empty string when there is none. An evdev
// node's datagram has no HID_UNIQ, and the HID device's add arrives
// before it.
func (d *devpathMACs) macOfParent(devpath string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for parent, mac := range d.macs {
		if strings.HasPrefix(devpath, parent+"/") {
			return mac
		}
	}
	return ""
}

// parseUevent splits a datagram of the form "action@devpath", then
// KEY=VALUE pairs, each part ending in a NUL byte. ok is false for any
// other datagram, such as a libudev message.
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

// kernelEventFrom returns false for every uevent the loop does not act
// on. A controller that connects sends many of them.
func kernelEventFrom(datagram []byte, macs *devpathMACs) (kernelEvent, bool) {
	action, devpath, values, ok := parseUevent(datagram)
	if !ok {
		return kernelEvent{}, false
	}
	switch values["SUBSYSTEM"] {
	case subsystemHID:
		if action != "add" && action != "remove" {
			return kernelEvent{}, false
		}
		mac := macs.resolve(action, devpath, values["HID_UNIQ"])
		if mac == "" {
			return kernelEvent{}, false
		}
		return kernelEvent{Subsystem: subsystemHID, Action: action, MAC: mac}, true
	case subsystemInput:
		// The relay opens only evdev nodes, so the inputN device and
		// a joydev node are ignored. A remove needs no event, because
		// the relay's read of the node fails when the kernel removes
		// it.
		if action != "add" || !strings.HasPrefix(values["DEVNAME"], "input/event") {
			return kernelEvent{}, false
		}
		mac := macs.macOfParent(devpath)
		if mac == "" {
			return kernelEvent{}, false
		}
		return kernelEvent{Subsystem: subsystemInput, Action: action, MAC: mac}, true
	case subsystemPowerSupply:
		// An add needs no event, because the power supply registers
		// while the HID device is added, and the HID add already woke
		// the loop.
		if action != "change" {
			return kernelEvent{}, false
		}
		return kernelEvent{Subsystem: subsystemPowerSupply, Action: action}, true
	}
	return kernelEvent{}, false
}

// listenForUevents opens the uevent socket and returns a channel of the
// events the loop acts on. The reader polls the socket and a pipe, and
// closing the pipe's write end when ctx ends wakes the poll. Closing
// the socket from another goroutine would not wake a poll that waits
// on it.
func listenForUevents(ctx context.Context) (<-chan kernelEvent, error) {
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
	events := make(chan kernelEvent, 16)
	go func() {
		<-ctx.Done()
		unix.Close(pipe[1])
	}()
	go readUevents(fd, pipe[0], events)
	return events, nil
}

// readUevents sends the events of fd's datagrams to events until
// cancelRead becomes readable. It closes both descriptors and the
// channel on return.
func readUevents(fd, cancelRead int, events chan<- kernelEvent) {
	defer unix.Close(fd)
	defer unix.Close(cancelRead)
	defer close(events)

	macs := newDevpathMACs()
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
			// A lost datagram can be a controller that connected or
			// disconnected, so wake the loop now instead of at the next
			// uevent or backstopInterval.
			if recvErrorLostAUevent(err) {
				sendEvent(events, kernelEvent{Lost: true})
			}
			continue
		}
		event, ok := kernelEventFrom(buf[:size], macs)
		if !ok {
			continue
		}
		sendEvent(events, event)
	}
}

// recvErrorLostAUevent reports whether a Recvfrom error can mean lost
// datagrams. EAGAIN and EINTR lose nothing. Every other error counts
// as a loss. The usual one is ENOBUFS: the receive buffer was full and
// the kernel dropped datagrams.
func recvErrorLostAUevent(err error) bool {
	return !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR)
}

// sendEvent drops event when the channel is full. The events already
// in the channel wake the loop, and a pass reads the whole state, so
// the dropped event loses nothing.
func sendEvent(events chan<- kernelEvent, event kernelEvent) {
	select {
	case events <- event:
	default:
	}
}
