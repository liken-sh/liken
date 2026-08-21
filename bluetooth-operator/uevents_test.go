package main

import (
	"strings"
	"testing"
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
		want     hidEvent
		wantOK   bool
	}{
		{
			name:     "a HID add names its controller",
			datagram: datagram("add@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac),
			want:     hidEvent{Action: "add", MAC: mac},
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, ok := hidEventFrom(c.datagram, newDevpathMACs())
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

	if _, ok := hidEventFrom(datagram("add@"+devpath, "SUBSYSTEM=hid", "HID_UNIQ="+mac), macs); !ok {
		t.Fatal("the add did not produce an event")
	}
	// A remove arrives after sysfs is gone. This one also drops
	// HID_UNIQ, so the map is the only record of which controller
	// left.
	event, ok := hidEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs)
	if !ok {
		t.Fatal("the remove did not produce an event")
	}
	if event != (hidEvent{Action: "remove", MAC: mac}) {
		t.Fatalf("event = %+v", event)
	}
	// The kernel reuses a DEVPATH for the next device in that slot, so
	// the removal clears the entry.
	if _, ok := hidEventFrom(datagram("remove@"+devpath, "SUBSYSTEM=hid"), macs); ok {
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
