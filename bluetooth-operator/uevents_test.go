package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// datagram frames a uevent the way the kernel does.
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
		t.Fatal("parseUevent rejected a well-formed datagram, want it parsed")
	}
	if action != "add" {
		t.Errorf("action is %q, want add", action)
	}
	if devpath != "/devices/virtual/misc/uhid/0005:054C:0CE6.0001" {
		t.Errorf("devpath is %q, want the uhid device path", devpath)
	}
	if values["SUBSYSTEM"] != "hid" || values["HID_UNIQ"] != "a0:ab:51:33:b7:12" {
		t.Errorf("values are %v, want SUBSYSTEM=hid and HID_UNIQ=a0:ab:51:33:b7:12", values)
	}
}

func TestParseUeventRejectsLibudevMessage(t *testing.T) {
	// libudev's messages share the socket and start with a magic
	// prefix instead of "action@devpath".
	if _, _, _, ok := parseUevent([]byte("libudev\x00\xfe\xed\xca\xfe")); ok {
		t.Fatal("parseUevent accepted a libudev message, want it rejected")
	}
	if _, _, _, ok := parseUevent(nil); ok {
		t.Fatal("parseUevent accepted an empty datagram, want it rejected")
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
				t.Fatalf("ok is %v, want %v", ok, c.wantOK)
			}
			if ok && event != c.want {
				t.Fatalf("event is %+v, want %+v", event, c.want)
			}
		})
	}
}

func TestHIDRemoveResolvesThroughTheMap(t *testing.T) {
	const devpath = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	const mac = "a0:ab:51:33:b7:12"
	macs := newDevpathMACs()

	if _, ok := kernelEventFrom(datagram("add@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac), macs); !ok {
		t.Fatal("the add datagram produced no event, want one")
	}
	// This remove has no HID_UNIQ.
	event, ok := kernelEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs)
	if !ok {
		t.Fatal("the remove datagram produced no event, want one")
	}
	if event != (kernelEvent{Subsystem: "hid", Action: "remove", MAC: mac}) {
		t.Fatalf("event is %+v, want the remove with the MAC recorded by the add", event)
	}
	// The remove deleted the entry, so a second remove finds no
	// address.
	if _, ok := kernelEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs); ok {
		t.Fatal("a second remove still produced an event, want none because the first remove deleted the entry")
	}
}

// An evdev node's uevent has no HID_UNIQ, so its controller comes from
// the HID device the node is under.
func TestInputEventFromResolvesControllerFromParentHIDDevice(t *testing.T) {
	const hid = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	const mac = "a0:ab:51:33:b7:12"

	cases := []struct {
		name     string
		datagram []byte
		want     kernelEvent
		wantOK   bool
	}{
		{
			name:     "an evdev node under a controller",
			datagram: datagram("add@"+hid+"/input/input21/event5", "SUBSYSTEM=input", "DEVNAME=input/event5"),
			want:     kernelEvent{Subsystem: "input", Action: "add", MAC: mac},
			wantOK:   true,
		},
		{
			name:     "the input device above the node",
			datagram: datagram("add@"+hid+"/input/input21", "SUBSYSTEM=input"),
			wantOK:   false,
		},
		{
			name:     "a joydev node",
			datagram: datagram("add@"+hid+"/input/input21/js0", "SUBSYSTEM=input", "DEVNAME=input/js0"),
			wantOK:   false,
		},
		{
			name:     "an evdev node that leaves",
			datagram: datagram("remove@"+hid+"/input/input21/event5", "SUBSYSTEM=input", "DEVNAME=input/event5"),
			wantOK:   false,
		},
		{
			name: "an evdev node under another HID device",
			datagram: datagram("add@/devices/virtual/misc/uhid/0005:054C:0CE6.00011/input/input22/event6",
				"SUBSYSTEM=input", "DEVNAME=input/event6"),
			wantOK: false,
		},
		{
			name:     "a USB keyboard's evdev node",
			datagram: datagram("add@/devices/pci0000:00/usb1/1-1/input/input3/event2", "SUBSYSTEM=input", "DEVNAME=input/event2"),
			wantOK:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			macs := newDevpathMACs()
			macs.resolve("add", hid, mac)
			event, ok := kernelEventFrom(c.datagram, macs)
			if ok != c.wantOK {
				t.Fatalf("ok is %v, want %v", ok, c.wantOK)
			}
			if ok && event != c.want {
				t.Fatalf("event is %+v, want %+v", event, c.want)
			}
		})
	}
}

func TestDevpathMACsPrefersTheDatagramToTheMap(t *testing.T) {
	const devpath = "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	macs := newDevpathMACs()
	// An earlier add recorded one controller's address for this path.
	macs.resolve("add", devpath, "A0:AB:51:33:B7:12")

	// A different controller now registers at the same path, and its
	// datagram carries its own address.
	if got := macs.resolve("add", devpath, "B4:8C:9D:11:22:33"); got != "b4:8c:9d:11:22:33" {
		t.Fatalf("resolve of the add is %q, want b4:8c:9d:11:22:33", got)
	}
	if got := macs.resolve("remove", devpath, ""); got != "b4:8c:9d:11:22:33" {
		t.Fatalf("resolve of the remove is %q, want b4:8c:9d:11:22:33", got)
	}
}

// Any error other than EAGAIN and EINTR counts as a loss, because poll
// reported the socket ready and the receive still returned no datagram.
func TestRecvErrorLostAUeventReportsLossForAllButEAGAINAndEINTR(t *testing.T) {
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

// The test passes a descriptor number the process never opened, because
// the runtime can reuse the number of a closed descriptor before the
// reader polls it. poll reports the unopened number with POLLNVAL, and
// Recvfrom on it fails with EBADF.
func TestReadUeventsSendsLostEventWhenReceiveFails(t *testing.T) {
	const neverOpened = 1 << 20
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatalf("creating the cancel pipe: %v", err)
	}
	events := make(chan kernelEvent, 1)
	go readUevents(neverOpened, pipe[0], events)

	select {
	case event := <-events:
		if !event.Lost {
			t.Errorf("event is %+v, want Lost set", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event with Lost set arrived within 5 seconds of a failed receive")
	}
	unix.Close(pipe[1])
	for range events {
	}
}
