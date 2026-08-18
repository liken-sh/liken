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

// cardPath is the devpath the kernel attaches a display hotplug to. A
// monitor plugged into HDMI-A-1 raises the event for the card, not for
// the connector, which is why the operator re-reads the whole of
// sysfs.
const cardPath = "/devices/pci0000:00/0000:00:02.0/drm/card1"

func TestParseUevent(t *testing.T) {
	action, devpath, values, ok := parseUevent(datagram(
		"change@"+cardPath,
		"ACTION=change",
		"DEVPATH="+cardPath,
		"SUBSYSTEM=drm",
		"HOTPLUG=1",
	))
	if !ok {
		t.Fatal("the datagram did not parse")
	}
	if action != "change" {
		t.Errorf("action = %q", action)
	}
	if devpath != cardPath {
		t.Errorf("devpath = %q", devpath)
	}
	if values["SUBSYSTEM"] != "drm" || values["HOTPLUG"] != "1" {
		t.Errorf("values = %v", values)
	}
}

func TestParseUeventRejectsALibudevMessage(t *testing.T) {
	// libudev's own messages share the socket and start with a magic
	// prefix instead of "action@devpath".
	if _, _, _, ok := parseUevent([]byte("libudev\x00\xfe\xed\xca\xfe")); ok {
		t.Fatal("a libudev message parsed as a uevent")
	}
	if _, _, _, ok := parseUevent(nil); ok {
		t.Fatal("an empty datagram parsed as a uevent")
	}
}

func TestDRMEventFrom(t *testing.T) {
	cases := []struct {
		name     string
		datagram []byte
		want     bool
	}{
		{
			name:     "a monitor plugged in or unplugged",
			datagram: datagram("change@"+cardPath, "SUBSYSTEM=drm", "HOTPLUG=1"),
			want:     true,
		},
		{
			name:     "the card itself arriving",
			datagram: datagram("add@"+cardPath, "SUBSYSTEM=drm"),
			want:     true,
		},
		{
			name:     "the card itself leaving",
			datagram: datagram("remove@"+cardPath, "SUBSYSTEM=drm"),
			want:     true,
		},
		{
			// Most of what a running machine puts on this socket is
			// another subsystem's, and the subsystem test drops all
			// of it.
			name:     "another subsystem",
			datagram: datagram("add@/devices/virtual/input/input5", "SUBSYSTEM=input"),
			want:     false,
		},
		{
			name:     "an action this operator ignores",
			datagram: datagram("bind@"+cardPath, "SUBSYSTEM=drm"),
			want:     false,
		},
		{
			name:     "a libudev message",
			datagram: []byte("libudev\x00\xfe\xed\xca\xfe"),
			want:     false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, ok := drmEventFrom(c.datagram)
			if ok != c.want {
				t.Fatalf("drmEventFrom = %+v, %v; want ok = %v", event, ok, c.want)
			}
			if ok && event.DevPath != cardPath {
				t.Errorf("devpath = %q", event.DevPath)
			}
		})
	}
}
