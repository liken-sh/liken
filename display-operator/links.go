package main

// The link on each connector, followed across passes. It answers one
// question, which output can serve nobody now, and the disconnected
// taint is what that answer writes.
//
// The case it exists for was measured on an A/V receiver. When the
// receiver switches its input, the HDMI link renegotiates: the
// connector goes dark for about 80 seconds and comes back carrying the
// same monitor. A taint inside that window ends every client on the
// screen for nothing.

import "time"

// How long a connector may be dark before its devices taint. The
// measured relink on an A/V receiver holds the connector dark for about
// 80 seconds while the 4K handshake settles, so the grace has to cover
// it. The cost is that a monitor that is really unplugged keeps its
// devices claimable for a minute and a half. It is a package-level var
// so the tests set their own.
var disconnectGrace = 90 * time.Second

// One connector's link, as the last pass left it.
type link struct {
	// The monitor id the connector carries, and while it is dark, the one
	// it left with. The mode is no part of it: the same panel back at
	// another mode is the same screen, and the claim on it asked for the
	// monitor.
	monitor string
	// When the connector went dark, or when a different monitor arrived on
	// it. It is the zero time on a connector that has been dark since
	// before this operator started, which is what makes such a connector
	// taint at once instead of holding a grace it never earned.
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
		out[i].Relinking, out[i].Replaced = history.follow(output)
	}
	return out
}

// follow records what this pass sees on one connector and answers the
// two facts sliceDevices taints on. A connector has four states here.
// One that has been dark since before the operator started taints at
// once. One that has just gone dark is relinking for the grace and
// taints nothing. One that has been dark longer than the grace taints.
// One that came back carrying a different monitor is replaced, and
// taints for one grace after the return, so the eviction controller
// sees it.
func (h *linkHistory) follow(output Output) (relinking, replaced bool) {
	now := h.now()
	was, seen := h.links[output.Connector]
	if !output.Connected {
		switch {
		case !seen:
			h.links[output.Connector] = link{}
		case was.lit:
			h.links[output.Connector] = link{monitor: was.monitor, since: now}
		}
		dark := h.links[output.Connector]
		return !dark.since.IsZero() && now.Sub(dark.since) <= disconnectGrace, false
	}
	monitor := monitorID(output.Monitor)
	switch {
	case seen && !was.lit && was.monitor != "" && monitor != "" && was.monitor != monitor:
		h.links[output.Connector] = link{monitor: monitor, since: now, lit: true, replaced: true}
		return false, true
	case was.replaced && now.Sub(was.since) <= disconnectGrace:
		h.links[output.Connector] = link{monitor: monitor, since: was.since, lit: true, replaced: true}
		return false, true
	default:
		h.links[output.Connector] = link{monitor: monitor, lit: true}
		return false, false
	}
}
