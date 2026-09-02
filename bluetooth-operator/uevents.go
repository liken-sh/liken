package main

// Listening for the kernel's uevents.
//
// The kernel broadcasts every device add and remove on a netlink
// socket, NETLINK_KOBJECT_UEVENT. Each datagram is "action@devpath"
// followed by KEY=VALUE pairs, each part ending in a NUL byte. A HID
// add tells this program that a controller's evdev nodes exist,
// moments before bluetoothd reports the connection over D-Bus.
//
// A power supply change tells the loop that a controller's battery
// reported a new level or a new charging status, so the Peripheral
// that reports it is rewritten on the pass that follows.
//
// Two ways to open this socket fail silently:
//
//   - Bind group 1, never group 2. Group 1 carries the kernel's own
//     broadcasts. Group 2 carries udev's re-broadcasts to libudev
//     clients. On a machine with no udev the bind to group 2
//     succeeds, the socket opens, and it delivers nothing forever,
//     because nothing writes to that group.
//   - Do not run in a non-init user namespace. The kernel delivers
//     uevents to the initial user namespace only, and a process in
//     its own user namespace receives an empty stream with no error
//     to read. An unprivileged process in the initial namespace may
//     receive group 1, because the kernel creates the uevent socket
//     with NL_CFG_F_NONROOT_RECV.
//
// A remove event arrives after the sysfs directory is already gone,
// so nothing can be read back from sysfs to name the controller that
// left. The listener keeps a map from DEVPATH to MAC address,
// populated at add time, and resolves a removal through it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

// kernelEvent reports one change the loop acts on: a Bluetooth HID
// device that appeared or left, with the peer address it resolved, or
// a power supply whose reading changed, which names no address because
// the datagram carries none and the pass re-reads sysfs anyway.
type kernelEvent struct {
	Subsystem string
	Action    string
	MAC       string
}

// The two subsystems this operator reads events from. hid carries a
// controller's arrival and departure, and power_supply carries a
// battery's change.
const (
	subsystemHID         = "hid"
	subsystemPowerSupply = "power_supply"
)

// devpathMACs remembers which controller registered each HID device,
// so a remove event can name the controller that left.
//
// A remove datagram does include HID_UNIQ on every kernel this program
// has run against, and the map makes the answer certain rather than
// probable. The lookup prefers the datagram's own value and falls
// back to the map, so a kernel that drops the key from a remove event
// still resolves.
type devpathMACs struct {
	mutex sync.Mutex
	macs  map[string]string
}

func newDevpathMACs() *devpathMACs {
	return &devpathMACs{macs: map[string]string{}}
}

// resolve names the controller behind one event. It returns an empty
// string when neither the datagram nor the map has a valid address,
// which happens for every HID device that is not a Bluetooth
// peripheral. A remove drops the DEVPATH from the map, because the
// kernel reuses the path for the next device in that slot.
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

// kernelEventFrom turns one datagram into an event, when the datagram
// is one of the two kinds the loop acts on, and reports false for
// every other uevent the kernel sends.
//
// The subsystem test drops every other subsystem's events. A
// controller that connects produces a HID add, an input add for each
// input device under it, and several more, and the operator needs one
// wake for the burst. Its settle window (main.go) collapses the rest.
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
	case subsystemPowerSupply:
		// A battery reports a new level or a new charging status as a change
		// on its power supply. An add is not a wake on its own, because the
		// supply registers during the HID probe that already woke the loop.
		if action != "change" {
			return kernelEvent{}, false
		}
		return kernelEvent{Subsystem: subsystemPowerSupply, Action: action}, true
	}
	return kernelEvent{}, false
}

// listenForUevents opens the kernel's uevent socket and returns a
// channel of the events this operator acts on. The channel is
// buffered, and a full channel drops the event: every consumer of this
// channel re-reads the whole of sysfs, so the events say when to look,
// never what is there.
//
// The socket is non-blocking. The reader waits for it in poll, not in
// a read, so it can also watch a cancel pipe in the same poll and
// stop the moment the context ends. This is liken's own arrangement,
// for the reason liken states: closing a descriptor does not wake a
// thread already blocked on a read of it.
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

// readUevents is the reader loop. It blocks in poll over the uevent
// socket and the cancel pipe. A ready socket means a datagram to
// read; a ready cancel pipe means the context is done and the loop
// returns. It closes the descriptors it owns as it leaves.
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
			// EAGAIN means the poll woke with no datagram to read. Any
			// other error left this datagram unread. A missed datagram
			// costs one late reconcile at worst, because the backstop
			// tick in main.go re-reads sysfs anyway.
			continue
		}
		event, ok := kernelEventFrom(buf[:size], macs)
		if !ok {
			continue
		}
		select {
		case events <- event:
		default:
		}
	}
}
