package main

// The link on each connector, followed across passes. It answers one
// question, which output can serve nobody now, and the disconnected
// taint is what that answer writes.
//
// A dark connector is not the same as an empty one. Two monitors
// measured here drop hot plug detect while they show another input: an
// A/V receiver renegotiates its HDMI link for about 80 seconds on every
// input change, and an LG ultrawide shares one HDMI receiver between
// its two HDMI inputs, so the input it is not showing looks unplugged
// for as long as the person leaves it that way. The kernel reports both
// exactly as it reports a cable somebody pulled out, and it clears the
// connector's EDID with them, so nothing in sysfs tells the two apart.
//
// So this history remembers. A connector that carried a monitor keeps
// carrying it while it is dark, and the slice keeps publishing that
// monitor's identity, so the claim on the screen still allocates and
// the pod on it keeps running. The cost is that a monitor somebody
// really unplugged keeps its devices claimable. The Connected condition
// on the Display is what still reports the wire.

import (
	"sync"
	"time"
)

// How long a connector that came back carrying a different monitor
// keeps its taint. The claim on it asked for the monitor that left, so
// the taint has to stand long enough for the eviction controller to
// read it. It is a package-level var so the tests set their own.
var disconnectGrace = 90 * time.Second

// One connector's link, as the last pass left it.
type link struct {
	// The monitor the connector carries, and while it is dark, the one
	// it left with. The mode is no part of it: the same panel back at
	// another mode is the same screen, and the claim on it asked for the
	// monitor.
	monitor EDID
	// When a different monitor arrived on the connector. It is the zero
	// time until one does.
	since time.Time
	lit   bool
	// The connector came back carrying a different monitor, and the taint
	// that says so still stands.
	replaced bool
}

// Every connector's link, keyed by the kernel's own connector name. One
// pass writes it once, and the operator holds one of these for as long
// as it runs.
type linkHistory struct {
	// The reconcile pass writes this history and the DRA plugin reads
	// it, on two goroutines, so every reader and writer takes the lock.
	mu    sync.Mutex
	now   func() time.Time
	links map[string]link
}

func newLinkHistory() *linkHistory {
	return &linkHistory{now: time.Now, links: map[string]link{}}
}

// withLinks puts each connector's history beside what sysfs said, the
// way withCurrentModes puts the card's readback there. It records this
// pass as it goes, so one pass calls it once.
func withLinks(outputs []Output, history *linkHistory) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].Remembered, out[i].Replaced = history.follow(output)
	}
	return out
}

// follow records what this pass sees on one connector and answers the
// two facts sliceDevices taints on: the monitor the connector still
// carries while it is dark, and whether it came back carrying a
// different one.
//
// A dark connector answers the monitor it left with, for as long as it
// stays dark. A connector this operator has never seen a monitor on
// answers none, which is what taints it: there is no screen to keep and
// no claim that ever named one. That covers a connector with nothing in
// it, and a machine that started with its panel on another input and
// has not seen it since.
//
// A connector that came back carrying a different monitor is replaced,
// and taints for one grace after the return, so the eviction controller
// sees it. The claim on it asked for the monitor that left.
func (h *linkHistory) follow(output Output) (remembered EDID, replaced bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	was, seen := h.links[output.Connector]
	if !output.Connected {
		if !seen {
			h.links[output.Connector] = link{}
		} else if was.lit {
			h.links[output.Connector] = link{monitor: was.monitor}
		}
		return h.links[output.Connector].monitor, false
	}
	monitor := monitorID(output.Monitor)
	previous := monitorID(was.monitor)
	switch {
	case seen && !was.lit && previous != "" && monitor != "" && previous != monitor:
		h.links[output.Connector] = link{monitor: output.Monitor, since: now, lit: true, replaced: true}
		return EDID{}, true
	case was.replaced && now.Sub(was.since) <= disconnectGrace:
		h.links[output.Connector] = link{monitor: output.Monitor, since: was.since, lit: true, replaced: true}
		return EDID{}, true
	default:
		h.links[output.Connector] = link{monitor: output.Monitor, lit: true}
		return EDID{}, false
	}
}

// remembered answers the monitor a connector still carries while it is
// dark, as the last pass left it. It answers the zero value for a lit
// connector, and for one this operator has never seen a monitor on.
//
// The prepare path reads it. A claim allocated against a screen that
// went dark between the allocation and the prepare must still be
// delivered, because the screen has not moved and the pod on it is the
// one that draws when the monitor comes back.
func (h *linkHistory) remembered(connector string) EDID {
	if h == nil {
		return EDID{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	was := h.links[connector]
	if was.lit {
		return EDID{}
	}
	return was.monitor
}
