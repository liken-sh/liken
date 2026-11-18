package main

// These tests send lines to the store from a fake module over a real
// socket, so the store holds only what its parser read from the wire.

import (
	"testing"
	"time"
)

func TestLayoutLinkStoresTheSurfacesTheModuleReports(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	// The socket identifies the claim. wayland-0 belongs to no claim.
	module.send("surface 7 wayland-claim-1 1920 1080")
	module.send("surface 8 wayland-0 640 480")
	waitUntil(t, "both surfaces arrive", func() bool { return len(link.state().Surfaces) == 2 })

	state := link.state()
	want := map[int]layoutSurface{
		7: {ID: 7, Socket: "wayland-claim-1", Width: 1920, Height: 1080},
		8: {ID: 8, Socket: "wayland-0", Width: 640, Height: 480},
	}
	for id, surface := range want {
		if state.Surfaces[id] != surface {
			t.Errorf("surface %d = %+v, want %+v", id, state.Surfaces[id], surface)
		}
	}

	// A resize keeps the surface's id and socket, and surface-gone
	// removes the surface from the store.
	module.send("surface-size 7 1344 1080")
	waitUntil(t, "the surface reports its new size", func() bool {
		return link.state().Surfaces[7].Width == 1344
	})
	if surface := link.state().Surfaces[7]; surface.Socket != "wayland-claim-1" || surface.Height != 1080 {
		t.Errorf("surface 7 = %+v, want the same socket and height", surface)
	}
	module.send("surface-gone 8")
	waitUntil(t, "the surface leaves the store", func() bool { return len(link.state().Surfaces) == 1 })
}

func TestLayoutLinkRemovesAnOutputThatLeaves(t *testing.T) {
	module := newFakeModule(t, moduleScript{greeting: []string{"output HDMI-A-1 1920 1080 2"}})
	link := servedLayoutLink(t, module)

	module.send("output-gone HDMI-A-1")
	waitUntil(t, "the output leaves the store", func() bool { return len(link.state().Outputs) == 0 })
}

func TestLayoutLinkWakesTheLoopOnEveryEvent(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)
	// Connecting also wakes the loop, so wait until those wakes stop
	// before counting the wake from the event.
	drainWakes(t, link.reports)

	module.send("surface 7 wayland-claim-1 1920 1080")
	waitForWake(t, link.reports, time.Second)
}

// drainWakes discards wakes from reports until none arrives for 100
// milliseconds.
func drainWakes(t *testing.T, reports <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-reports:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestLayoutLinkDropsAnEventItCannotRead(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	// None of these lines can be read: an unknown first word, a wrong
	// number of fields, a size that is not a number, a size for a
	// surface that never arrived, and an id that is not a number.
	for _, line := range []string{
		"pipe 7 HDMI-A-1",
		"output HDMI-A-1 1920 1080",
		"surface 7 wayland-claim-1 wide 1080",
		"surface-size 9 640 480",
		"surface-gone eight",
		"",
	} {
		module.send(line)
	}
	// The store handles lines in order, so once this valid line has
	// taken effect, the lines before it have been read and dropped.
	module.send("output HDMI-A-1 1920 1080 2")
	waitUntil(t, "the readable event arrives", func() bool { return len(link.state().Outputs) == 1 })

	if surfaces := link.state().Surfaces; len(surfaces) != 0 {
		t.Errorf("the store holds surfaces %+v, want none because every surface line is malformed", surfaces)
	}
}
