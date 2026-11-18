package main

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// datagram builds a uevent datagram in the kernel's format, with a NUL
// byte after each part.
func datagram(header string, pairs ...string) []byte {
	return []byte(header + "\x00" + strings.Join(pairs, "\x00") + "\x00")
}

// cardPath is a card's devpath. The kernel sends a display hotplug on
// the card, not on the connector.
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
		t.Errorf("the action is %q, want \"change\"", action)
	}
	if devpath != cardPath {
		t.Errorf("the devpath is %q, want %q", devpath, cardPath)
	}
	if values["SUBSYSTEM"] != "drm" || values["HOTPLUG"] != "1" {
		t.Errorf("the values are %v, want SUBSYSTEM=drm and HOTPLUG=1", values)
	}
}

func TestParseUeventRejectsLibudevAndEmptyDatagrams(t *testing.T) {
	// libudev messages arrive on the same socket and start with a magic
	// prefix, not "action@devpath".
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
				t.Fatalf("drmEventFrom(%q) = %+v, %v, want ok = %v", c.datagram, event, ok, c.want)
			}
			if ok && event.DevPath != cardPath {
				t.Errorf("the devpath is %q, want %q", event.DevPath, cardPath)
			}
		})
	}
}

func TestLostEventsProducesAnEventForErrorsThatDropDatagrams(t *testing.T) {
	cases := []struct {
		err   error
		wakes bool
	}{
		{unix.ENOBUFS, true},
		{fmt.Errorf("receiving: %w", unix.ENOBUFS), true},
		{unix.EBADF, true},
		{unix.EAGAIN, false},
		{unix.EINTR, false},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			event, wakes := lostUevents(c.err)
			if wakes != c.wakes {
				t.Fatalf("the error %v woke the loop: %v, want %v", c.err, wakes, c.wakes)
			}
			if wakes && !strings.Contains(event.DevPath, c.err.Error()) {
				t.Errorf("the event is %+v, want it to include the error %q", event, c.err)
			}
		})
	}
}

// A hotplug hold reads Connector to tell which connector changed.
func TestADrmEventKeepsTheConnectorIdOfAHotplug(t *testing.T) {
	event, ok := drmEventFrom(datagram("change@"+cardPath, "SUBSYSTEM=drm", "HOTPLUG=1", "CONNECTOR=403"))
	if !ok || event.Connector != "403" {
		t.Errorf("drmEventFrom = %+v, %v, want ok and the connector 403", event, ok)
	}
}
