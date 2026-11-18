package main

// The link history records the last monitor read on each connector,
// so the operator can tell a monitor that shows another input from a
// monitor that was unplugged. sliceDevices decides from it which
// connectors get the disconnected taint.
//
// Some monitors drop hot plug detect while they show another input. An
// A/V receiver drops it while it renegotiates its HDMI link after each
// input change. An LG ultrawide shares one HDMI receiver between its
// two HDMI inputs, so the input it is not showing reads as unplugged
// until someone switches back to it. The kernel reports each of these
// as a pulled cable and clears the connector's EDID, so nothing in
// sysfs tells them apart.
//
// So while a connector reads disconnected, the history keeps the
// monitor it last had. The slice keeps publishing that monitor, a claim
// on it still allocates, and the pod on it keeps running. The cost is
// that a monitor that was really unplugged keeps its devices claimable.
// The Connected condition on the Display still reports what the kernel
// reads.

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// disconnectGrace is how long a connector keeps the taint after it
// reconnects with a different monitor. The claim on the connector asked
// for the monitor that left, so the taint must last until the eviction
// controller reads it and evicts the pod.
var disconnectGrace = 90 * time.Second

// link is the history of one connector.
type link struct {
	// monitor is the last monitor read on the connector. It stays set
	// while the connector is disconnected.
	monitor EDID
	// since is when the connector reconnected with a different monitor.
	// It is zero while replaced is false.
	since time.Time
	// connected is Output.Connected from the last pass.
	connected bool
	// replaced is true for disconnectGrace after the connector
	// reconnected with a different monitor.
	replaced bool
}

// linkHistory holds each connector's link, keyed by the kernel's
// connector name. It is in memory only, so a restarted operator starts
// with an empty history.
type linkHistory struct {
	// The reconcile pass writes the history and the DRA plugin reads it
	// from another goroutine.
	mu    sync.Mutex
	now   func() time.Time
	links map[string]link
}

func newLinkHistory() *linkHistory {
	return &linkHistory{now: time.Now, links: map[string]link{}}
}

// withLinks returns the outputs with Remembered and Replaced set from
// the link history, and records each output in the history.
func withLinks(outputs []Output, history *linkHistory) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].Remembered, out[i].Replaced = history.follow(output)
	}
	return out
}

// follow records what this pass read on one connector. It returns the
// monitor to publish for a disconnected connector, and whether the
// connector is replaced, which means that it reconnected with a
// different monitor less than disconnectGrace ago.
//
// A disconnected connector returns the last monitor it had, for as long
// as it stays disconnected. A connector with no known monitor returns
// none, so sliceDevices taints it. That is an empty connector, or a
// connector that has been disconnected since the operator started and
// that loadFromDisplays found no Display for.
//
// Monitors are compared by monitorID, which leaves out the mode and the
// serial. A monitor with no readable EDID has an empty monitorID, and
// it never makes a connector replaced.
func (h *linkHistory) follow(output Output) (remembered EDID, replaced bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	was, seen := h.links[output.Connector]
	if !output.Connected {
		if !seen {
			h.links[output.Connector] = link{}
		} else if was.connected {
			h.links[output.Connector] = link{monitor: was.monitor}
		}
		return h.links[output.Connector].monitor, false
	}
	monitor := monitorID(output.Monitor)
	previous := monitorID(was.monitor)
	switch {
	case seen && !was.connected && previous != "" && monitor != "" && previous != monitor:
		h.links[output.Connector] = link{monitor: output.Monitor, since: now, connected: true, replaced: true}
		return EDID{}, true
	case was.replaced && now.Sub(was.since) <= disconnectGrace:
		h.links[output.Connector] = link{monitor: output.Monitor, since: was.since, connected: true, replaced: true}
		return EDID{}, true
	default:
		h.links[output.Connector] = link{monitor: output.Monitor, connected: true}
		return EDID{}, false
	}
}

// remembered returns the last monitor of a disconnected connector. It
// returns the zero EDID for a connected connector and for one with no
// known monitor.
//
// The DRA prepare reads it, so a claim allocated to a screen that
// disconnected before the prepare still succeeds. The pod draws again
// when the monitor reconnects.
func (h *linkHistory) remembered(connector string) EDID {
	if h == nil {
		return EDID{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	was := h.links[connector]
	if was.connected {
		return EDID{}
	}
	return was.monitor
}

// loadFromDisplays loads the monitors that this node's Display
// resources record into the history, for each connector that the
// history has no entry for.
//
// A machine that boots with its panel on another input has no EDID in
// sysfs, so after a restart nothing on the machine records which
// monitor belongs to that connector, and a claim on the screen cannot
// allocate. Each Display records the node and connector it was last
// published on, and its name is the monitorID that claims select on.
//
// loadFromDisplays skips a connector that is connected now, because
// the kernel's reading is newer than the Display. It also skips a
// monitor that is connected on another connector. That monitor moved,
// and publishing it on both connectors would give a claim two matching
// devices.
func (h *linkHistory) loadFromDisplays(displays []Display, node string, outputs []Output) {
	connectedNow := map[string]bool{}
	elsewhere := map[string]bool{}
	for _, output := range connected(outputs) {
		connectedNow[output.Connector] = true
		if id := monitorID(output.Monitor); id != "" {
			elsewhere[id] = true
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, display := range displays {
		connector := display.Status.Connector
		if display.Status.Node != node || connector == "" {
			continue
		}
		if connectedNow[connector] || elsewhere[display.Metadata.Name] {
			continue
		}
		if _, known := h.links[connector]; known {
			continue
		}
		monitor, rebuilt := rememberedMonitor(display)
		if !rebuilt {
			continue
		}
		h.links[connector] = link{monitor: monitor}
	}
}

// rememberedMonitor rebuilds the monitor that a Display was published
// for. The product code is in the Display's name and in no status
// field, so it parses the code from the name and takes the other fields
// from the status. It returns false when the monitorID of the result is
// not the Display's name, because then the result is not the monitor
// that claims on that name selected.
func rememberedMonitor(display Display) (EDID, bool) {
	name := display.Metadata.Name
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 2 {
		return EDID{}, false
	}
	code, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return EDID{}, false
	}
	monitor := EDID{
		Manufacturer: display.Status.Manufacturer,
		ProductCode:  uint16(code),
		ModelName:    display.Status.Model,
		Serial:       display.Status.Serial,
	}
	if monitorID(monitor) != name {
		return EDID{}, false
	}
	return monitor, true
}
