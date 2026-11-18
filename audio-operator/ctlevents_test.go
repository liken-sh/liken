package main

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
	"unsafe"
)

// parseControlEvent reads fields at fixed offsets, so a layout that
// differs from the kernel's struct snd_ctl_event reads every field from
// the wrong offset.
func TestControlEventSize(t *testing.T) {
	if got := unsafe.Sizeof(ctlEvent{}); got != ctlEventSize {
		t.Errorf("sizeof(snd_ctl_event) = %d, want %d", got, ctlEventSize)
	}
	if got := unsafe.Offsetof(ctlEvent{}.ID); got != ctlEventIDOffset {
		t.Errorf("offsetof(snd_ctl_event.data.elem.id) = %d, want %d", got, ctlEventIDOffset)
	}
}

// SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS is _IOWR('U', 0x16, int).
func TestSubscribeIoctlNumber(t *testing.T) {
	if got, want := ctlIoctlSubscribeEvents, uintptr(0xc0045516); got != want {
		t.Errorf("SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS = %#x, want %#x", got, want)
	}
}

func eventFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/mixer/" + name)
	if err != nil {
		t.Fatalf("reading the event fixture: %v", err)
	}
	if len(raw) != ctlEventSize {
		t.Fatalf("the fixture %s has %d bytes, want %d, the size of one record", name, len(raw), ctlEventSize)
	}
	return raw
}

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

// A removed element has every mask bit set, so the value bit alone does
// not mean that the value changed.
func TestRemovedElement(t *testing.T) {
	raw := eventFixture(t, "event-master-playback-volume.bin")
	copy(raw[4:8], []byte{0xff, 0xff, 0xff, 0xff})

	event := parseControlEvent(1, raw)
	if !event.removed() {
		t.Error("removed() = false, want true because the mask has every bit set")
	}
	if event.valueChanged() {
		t.Error("valueChanged() = true for a removed element, want false")
	}
}

// The test reads from a pipe in place of a control device, because only
// the subscribe ioctl needs a real one, and readControlEvents does not
// call it.
func TestReadControlEventsEndsWithTheContext(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("opening a pipe: %v", err)
	}
	defer writer.Close()

	ctx, cancel := context.WithCancel(t.Context())
	events := readControlEvents(ctx, 0, reader)

	if _, err := writer.Write(eventFixture(t, "event-master-playback-volume.bin")); err != nil {
		t.Fatalf("writing the event record to the pipe: %v", err)
	}
	select {
	case event := <-events:
		if event.Name != "Master Playback Volume" {
			t.Errorf("the event names %q, want %q", event.Name, "Master Playback Volume")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived within five seconds")
	}

	cancel()
	select {
	case _, open := <-events:
		if open {
			t.Error("the channel delivered an event after the context ended, want it closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel stayed open five seconds after the context ended")
	}
}

// The test runs the subscribe ioctl and the non-blocking read on a real
// card, so it runs only on a machine that has card 0.
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

func TestControlCardsListsTheDeliveredControlNodes(t *testing.T) {
	sndDirWith(t, "controlC2", "controlC0", "pcmC0D0p", "pcmC2D3p", "timer")
	if got := controlCards(); !slices.Equal(got, []int{0, 2}) {
		t.Errorf("controlCards = %v, want [0 2]", got)
	}
}

// A reader that stops on its own calls wake, so the next pass starts a
// new reader and reads what changed while no reader ran. A reader that
// follow stopped, for a card that is gone, does not call wake.
func TestAReaderThatStopsOnItsOwnWakesTheLoop(t *testing.T) {
	cases := []struct {
		name     string
		followed bool
		wakes    int
	}{
		{"the reader ended on an error", true, 1},
		{"follow stopped the reader", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wakes := 0
			watchers := watchCards(t.Context(), func() { wakes++ })
			reader := &cardReader{stop: func() {}}
			if c.followed {
				watchers.watching[3] = reader
			}
			ended := make(chan controlEvent)
			close(ended)
			watchers.relay(3, reader, ended)

			if wakes != c.wakes {
				t.Errorf("the relay woke the loop %d times, want %d", wakes, c.wakes)
			}
			if _, watching := watchers.watching[3]; watching {
				t.Error("the stopped card is still in the watchers, want it removed")
			}
		})
	}
}
