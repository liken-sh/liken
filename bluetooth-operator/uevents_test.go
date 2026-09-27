package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// datagram builds one uevent datagram the way the kernel frames it:
// "action@devpath", then KEY=VALUE pairs, with a NUL byte after every
// part.
func datagram(header string, pairs ...string) []byte {
	return []byte(header + "\x00" + strings.Join(pairs, "\x00") + "\x00")
}

func TestParseUevent(t *testing.T) {
	action, devpath, values, ok := parseUevent(datagram(
		"add@/devices/virtual/misc/uhid/0005:054C:0CE6.0001",
		"ACTION=add",
		"SUBSYSTEM=hid",
		"HID_UNIQ=a0:ab:51:33:b7:12",
	))
	if !ok {
		t.Fatal("the datagram did not parse")
	}
	if action != "add" {
		t.Errorf("action = %q", action)
	}
	if devpath != "/devices/virtual/misc/uhid/0005:054C:0CE6.0001" {
		t.Errorf("devpath = %q", devpath)
	}
	if values["SUBSYSTEM"] != "hid" || values["HID_UNIQ"] != "a0:ab:51:33:b7:12" {
		t.Errorf("values = %v", values)
	}
}

func TestParseUeventRejectsLibudevMessage(t *testing.T) {
	// libudev's own messages share the socket and start with a magic
	// prefix instead of "action@devpath".
	if _, _, _, ok := parseUevent([]byte("libudev\x00\xfe\xed\xca\xfe")); ok {
		t.Fatal("a libudev message parsed as a uevent")
	}
	if _, _, _, ok := parseUevent(nil); ok {
		t.Fatal("an empty datagram parsed as a uevent")
	}
}

func TestHIDEventFrom(t *testing.T) {
	const devpath = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	const mac = "a0:ab:51:33:b7:12"

	cases := []struct {
		name     string
		datagram []byte
		want     kernelEvent
		wantOK   bool
	}{
		{
			name:     "a HID add names its controller",
			datagram: datagram("add@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac),
			want:     kernelEvent{Subsystem: "hid", Action: "add", MAC: mac},
			wantOK:   true,
		},
		{
			name:     "an input add is not a HID event",
			datagram: datagram("add@"+devpath+"/input/input5", "SUBSYSTEM=input", "HID_UNIQ="+mac),
			wantOK:   false,
		},
		{
			name:     "a change is neither an add nor a remove",
			datagram: datagram("change@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac),
			wantOK:   false,
		},
		{
			name:     "a HID device with no address is not a controller",
			datagram: datagram("add@/devices/pci0000:00/0003:046D:C31C.0002", "SUBSYSTEM=hid"),
			wantOK:   false,
		},
		{
			name: "a battery reports a new level as a change",
			datagram: datagram("change@"+devpath+"/power_supply/ps-controller-battery-"+mac,
				"SUBSYSTEM=power_supply", "POWER_SUPPLY_NAME=ps-controller-battery-"+mac,
				"POWER_SUPPLY_CAPACITY=40"),
			want:   kernelEvent{Subsystem: "power_supply", Action: "change"},
			wantOK: true,
		},
		{
			name: "a power supply that appears is not a level change",
			datagram: datagram("add@"+devpath+"/power_supply/ps-controller-battery-"+mac,
				"SUBSYSTEM=power_supply"),
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, ok := kernelEventFrom(c.datagram, newDevpathMACs())
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && event != c.want {
				t.Fatalf("event = %+v, want %+v", event, c.want)
			}
		})
	}
}

func TestHIDRemoveResolvesThroughTheMap(t *testing.T) {
	const devpath = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	const mac = "a0:ab:51:33:b7:12"
	macs := newDevpathMACs()

	if _, ok := kernelEventFrom(datagram("add@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac), macs); !ok {
		t.Fatal("the add did not produce an event")
	}
	// A remove arrives after sysfs is gone. This one also drops
	// HID_UNIQ, so the map is the only record of which controller
	// left.
	event, ok := kernelEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs)
	if !ok {
		t.Fatal("the remove did not produce an event")
	}
	if event != (kernelEvent{Subsystem: "hid", Action: "remove", MAC: mac}) {
		t.Fatalf("event = %+v", event)
	}
	// The kernel reuses a DEVPATH for the next device in that slot, so
	// the removal clears the entry.
	if _, ok := kernelEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs); ok {
		t.Fatal("a second remove still resolved to a controller")
	}
}

func TestDevpathMACsPrefersTheDatagram(t *testing.T) {
	const devpath = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	macs := newDevpathMACs()
	// An earlier add stored one controller's address for this path.
	macs.resolve("add", devpath, "A0:AB:51:33:B7:12")

	// The same sysfs path now holds a different controller, and the
	// datagram says so.
	if got := macs.resolve("add", devpath, "B4:8C:9D:11:22:33"); got != "b4:8c:9d:11:22:33" {
		t.Fatalf("resolve = %q, want b4:8c:9d:11:22:33", got)
	}
	if got := macs.resolve("remove", devpath, ""); got != "b4:8c:9d:11:22:33" {
		t.Fatalf("resolve = %q, want the recorded address", got)
	}
}

// EAGAIN and EINTR leave nothing unread. ENOBUFS is the case the check
// exists for: the kernel's receive buffer overflowed, and it dropped
// datagrams. Every other error gets the same answer as ENOBUFS, because
// poll reported the socket ready and the call returned no datagram. The
// wrapped case proves the check reads through %w.
func TestRecvErrorLostAUevent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "EAGAIN", err: unix.EAGAIN, want: false},
		{name: "EINTR", err: unix.EINTR, want: false},
		{name: "ENOBUFS", err: unix.ENOBUFS, want: true},
		{name: "a wrapped ENOBUFS", err: fmt.Errorf("recvfrom: %w", unix.ENOBUFS), want: true},
		{name: "an unrelated error", err: unix.EBADF, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := recvErrorLostAUevent(c.err); got != c.want {
				t.Errorf("recvErrorLostAUevent(%v) = %t, want %t", c.err, got, c.want)
			}
		})
	}
}

// The reader wakes the loop when a receive loses a datagram. The test
// uses a descriptor number the process never opened, not one it
// closed: the runtime can reuse a closed number before the reader
// polls it. poll reports the never-opened number ready with POLLNVAL,
// and Recvfrom on it fails with EBADF, which takes the same path as
// ENOBUFS.
func TestReadUeventsWakesOnALostDatagram(t *testing.T) {
	const neverOpened = 1 << 20
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	events := make(chan kernelEvent, 1)
	go readUevents(neverOpened, pipe[0], events)

	select {
	case event := <-events:
		if !event.Lost {
			t.Errorf("event = %+v, want a lost datagram", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a lost datagram did not wake the loop")
	}
	unix.Close(pipe[1])
	for range events {
	}
}
