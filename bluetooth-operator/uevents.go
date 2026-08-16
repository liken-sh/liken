package main

// Listening for the kernel's HID events.
//
// The kernel broadcasts every device add and remove on a netlink
// socket, NETLINK_KOBJECT_UEVENT. Each datagram is "action@devpath"
// followed by KEY=VALUE pairs, each part ending in a NUL byte. A HID
// add is what tells this program that a controller's evdev nodes
// exist, moments before bluetoothd reports the connection over D-Bus.
//
// Two traps around the socket fail silently, and both are worth
// writing down.
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

// hidEvent reports that one Bluetooth HID device appeared or
// disappeared. Action is the kernel's word, "add" or "remove".
type hidEvent struct {
	Action string
	MAC    string
}

// devpathMACs remembers which controller registered each HID device,
// so a remove event can name the controller that left.
//
// A remove datagram does carry HID_UNIQ in the kernels this program
// has met, and the map is what makes the answer certain rather than
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

// record stores the address an add event reported for a DEVPATH.
func (d *devpathMACs) record(devpath, mac string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.macs[devpath] = mac
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

// hidEventFrom turns one datagram into an event, when the datagram
// reports a HID device appearing or disappearing. Everything else on
// the socket, which on a running machine is most of it, reports false.
//
// The subsystem test is what keeps the volume down. A controller that
// connects produces a HID add, an input add for each input device
// under it, and several more, and the operator needs one wake for the
// burst. Its settle window (main.go) collapses the rest.
func hidEventFrom(datagram []byte, macs *devpathMACs) (hidEvent, bool) {
	action, devpath, values, ok := parseUevent(datagram)
	if !ok {
		return hidEvent{}, false
	}
	if action != "add" && action != "remove" {
		return hidEvent{}, false
	}
	if values["SUBSYSTEM"] != "hid" {
		return hidEvent{}, false
	}
	mac := macs.resolve(action, devpath, values["HID_UNIQ"])
	if mac == "" {
		return hidEvent{}, false
	}
	return hidEvent{Action: action, MAC: mac}, true
}

// listenForUevents opens the kernel's uevent socket and returns a
// channel of HID events. The channel is buffered, and a full channel
// drops the event: every consumer of this channel re-reads the whole
// of sysfs, so the events say when to look, never what is there.
//
// The socket is non-blocking. The reader waits for it in poll, not in
// a read, so it can also watch a cancel pipe in the same poll and
// stop the moment the context ends. This is liken's own arrangement,
// for the reason liken states: closing a descriptor does not wake a
// thread already blocked on a read of it.
func listenForUevents(ctx context.Context) (<-chan hidEvent, error) {
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
	events := make(chan hidEvent, 16)
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
func readUevents(fd, cancelRead int, events chan<- hidEvent) {
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
		event, ok := hidEventFrom(buf[:size], macs)
		if !ok {
			continue
		}
		select {
		case events <- event:
		default:
		}
	}
}
