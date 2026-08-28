package main

import (
	"context"
	"os"
	"testing"
	"time"
	"unsafe"
)

// The read on the control device returns whole records, so a
// structure that disagrees with the kernel's reads every field of
// every event at the wrong offset.
func TestControlEventSize(t *testing.T) {
	if got := unsafe.Sizeof(ctlEvent{}); got != ctlEventSize {
		t.Errorf("sizeof(snd_ctl_event) = %d, want %d", got, ctlEventSize)
	}
	if got := unsafe.Offsetof(ctlEvent{}.ID); got != ctlEventIDOffset {
		t.Errorf("offsetof(snd_ctl_event.data.elem.id) = %d, want %d", got, ctlEventIDOffset)
	}
}

// The subscribe request number, whose argument is one int.
func TestSubscribeIoctlNumber(t *testing.T) {
	if got, want := ctlIoctlSubscribeEvents, uintptr(0xc0045516); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS = %#x, want %#x", got, want)
	}
}

// eventFixture reads one record the way the control device delivers it.
func eventFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/mixer/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != ctlEventSize {
		t.Fatalf("the fixture holds %d bytes, and a record is %d", len(raw), ctlEventSize)
	}
	return raw
}

// The record names the element that changed and what changed
// about it.
func TestParseControlEvent(t *testing.T) {
	event := parseControlEvent(0, eventFixture(t, "event-master-playback-volume.bin"))
	cases := []struct {
		name      string
		got, want any
	}{
		{name: "card", got: event.Card, want: 0},
		{name: "numid", got: event.NumID, want: uint32(5)},
		{name: "interface", got: event.Interface, want: int32(ctlElemIfaceMixer)},
		{name: "device", got: event.Device, want: uint32(0)},
		{name: "index", got: event.Index, want: uint32(0)},
		{name: "name", got: event.Name, want: "Master Playback Volume"},
		{name: "mask", got: event.Mask, want: uint32(ctlEventMaskValue)},
		{name: "the value changed", got: event.valueChanged(), want: true},
		{name: "the element is still there", got: event.removed(), want: false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if got, want := event.String(),
		"card 0: Master Playback Volume (numid 5, index 0) changed, mask 0x1"; got != want {
		t.Errorf("the log line is %q, want %q", got, want)
	}
}

// A removed element carries every mask bit, so the value bit
// alone does not say that a value changed.
func TestRemovedElement(t *testing.T) {
	raw := eventFixture(t, "event-master-playback-volume.bin")
	copy(raw[4:8], []byte{0xff, 0xff, 0xff, 0xff})

	event := parseControlEvent(1, raw)
	if !event.removed() {
		t.Error("the element reads as present, and the mask says it was removed")
	}
	if event.valueChanged() {
		t.Error("a removed element reads as a value change")
	}
}

// The reader carries whole records onto the channel and ends
// when the context does. A pipe stands in for the control device,
// because the subscribe ioctl is the only part a pipe cannot answer.
func TestReadControlEventsEndsWithTheContext(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	ctx, cancel := context.WithCancel(t.Context())
	events := readControlEvents(ctx, 0, reader)

	if _, err := writer.Write(eventFixture(t, "event-master-playback-volume.bin")); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Name != "Master Playback Volume" {
			t.Errorf("the event names %q", event.Name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived within five seconds")
	}

	cancel()
	select {
	case _, open := <-events:
		if open {
			t.Error("the channel carried an event after the context ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel stayed open five seconds after the context ended")
	}
}

// The live check. The subscribe ioctl and the non-blocking read
// run only where a card answers.
func TestWatchControlsOnTheLocalCard(t *testing.T) {
	sndDir = "/dev/snd"
	t.Cleanup(func() { sndDir = "/dev/snd" })
	if _, err := os.Stat(sndDir + "/controlC0"); err != nil {
		t.Skip("this machine has no card 0 to watch")
	}

	ctx, cancel := context.WithCancel(t.Context())
	events, err := watchControls(ctx, 0)
	if err != nil {
		t.Skipf("card 0 does not subscribe for this user: %v", err)
	}

	cancel()
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("the channel stayed open five seconds after the context ended")
	}
}
