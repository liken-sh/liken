package main

// Listening for the kernel's uevents.
//
// The kernel broadcasts every device add and remove on a netlink
// socket, NETLINK_KOBJECT_UEVENT. Each datagram is "action@devpath"
// followed by KEY=VALUE pairs, each part ending in a NUL byte. A HID
// add tells this program that a controller connected, moments before
// bluetoothd reports the connection over D-Bus. The HID driver then
// registers the controller's evdev nodes, and the kernel sends an
// input add for each one. The relay opens a node on its input add and
// does not wait for the settle window, because the kernel keeps no
// backlog for a reader that opens a node late.
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
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// kernelEvent reports one change the loop acts on: a Bluetooth HID
// device that appeared or left, or an evdev node that appeared under
// one, each with the peer address it resolved. It also reports a power
// supply whose reading changed, which names no address because the
// datagram carries none and the pass re-reads sysfs anyway.
//
// Lost marks the third kind: a receive that lost datagrams. It names
// no subsystem, because the lost datagrams could be any of them, and
// the pass that follows reads the whole state.
type kernelEvent struct {
	Subsystem string
	Action    string
	MAC       string
	Lost      bool
}

// The three subsystems this operator reads events from. hid carries a
// controller's arrival and departure, input carries each evdev node the
// controller registers, and power_supply carries a battery's change.
const (
	subsystemHID         = "hid"
	subsystemInput       = "input"
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

// under names the controller whose HID device holds a device below it
// in sysfs, such as an evdev node. The node's own datagram carries no
// HID_UNIQ, and the HID device's add came first, so the map holds the
// address. It returns an empty string for a device under no controller.
func (d *devpathMACs) under(devpath string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for parent, mac := range d.macs {
		if strings.HasPrefix(devpath, parent+"/") {
			return mac
		}
	}
	return ""
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
// is one of the kinds the loop acts on, and reports false for every
// other uevent the kernel sends.
//
// The tests drop every other subsystem's events, and every input
// device that is not a controller's evdev node. A controller that
// connects produces a HID add, an input add for each input device and
// each node under it, and several more. The relay opens each node on
// its own event (main.go), and the settle window collapses the burst
// into one pass.
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
		// The inputN device above a node and a joydev node are not
		// nodes the relay opens, and a node that leaves ends the read
		// of the relay's pump on its own.
		if action != "add" || !strings.HasPrefix(values["DEVNAME"], "input/event") {
			return kernelEvent{}, false
		}
		mac := macs.under(devpath)
		if mac == "" {
			return kernelEvent{}, false
		}
		return kernelEvent{Subsystem: subsystemInput, Action: action, MAC: mac}, true
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
			// A lost datagram can be a controller that connected or left,
			// so the loop runs a pass now, and does not wait for the next
			// uevent or the backstop tick. EAGAIN and EINTR lose nothing,
			// so they wake nothing.
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

// recvErrorLostAUevent reports whether a Recvfrom error on the uevent
// socket lost datagrams. EAGAIN means poll woke with nothing queued,
// and EINTR means a signal interrupted the call before it read
// anything. Every other error loses datagrams, most often ENOBUFS: the
// kernel's receive buffer overflowed, and the kernel dropped the
// datagrams that did not fit.
func recvErrorLostAUevent(err error) bool {
	return !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR)
}

// sendEvent sends one event and drops it when the channel is full. A
// full channel already holds a wake, and the pass that the wake starts
// reads the whole state, so the dropped event loses nothing.
func sendEvent(events chan<- kernelEvent, event kernelEvent) {
	select {
	case events <- event:
	default:
	}
}
