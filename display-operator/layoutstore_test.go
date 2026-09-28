package main

// These tests drive the store from the module's own events. The
// events arrive on a real socket, so what the store holds is what a
// line off the wire put there.

import (
	"testing"
	"time"
)

func TestLayoutLinkStoresTheSurfacesTheModuleReports(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)

	// The socket names the claim, and wayland-0 names none of them.
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

	// A surface that commits a buffer of a new size keeps its id and
	// its socket, and one that goes leaves the store.
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

func TestLayoutLinkForgetsAnOutputThatLeaves(t *testing.T) {
	module := newFakeModule(t, moduleScript{greeting: []string{"output HDMI-A-1 1920 1080 2"}})
	link := servedLayoutLink(t, module)

	module.send("output-gone HDMI-A-1")
	waitUntil(t, "the output leaves the store", func() bool { return len(link.state().Outputs) == 0 })
}

func TestLayoutLinkWakesTheLoopOnEveryEvent(t *testing.T) {
	module := newFakeModule(t, moduleScript{})
	link := servedLayoutLink(t, module)
	// The connection itself wakes the loop, so the test waits for
	// quiet before it counts the event's own wake.
	drainWakes(t, link.reports)

	module.send("surface 7 wayland-claim-1 1920 1080")
	waitForWake(t, link.reports, time.Second)
}

// drainWakes reads the wakes a new connection sent and returns when
// the channel has been quiet for a moment.
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

	// Each line below is one the store must not take: a verb this
	// operator does not read, a count the protocol does not state, a
	// size that is not a number, and a size for a surface that has
	// never arrived.
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
	// One event the store does take, sent last, is what says every
	// line before it was read and dropped.
	module.send("output HDMI-A-1 1920 1080 2")
	waitUntil(t, "the readable event arrives", func() bool { return len(link.state().Outputs) == 1 })

	if surfaces := link.state().Surfaces; len(surfaces) != 0 {
		t.Errorf("the store holds %+v, and every surface line was unreadable", surfaces)
	}
}
