package main

// The hold on a connector whose hot plug detect drops for a moment.
//
// An A/V receiver in standby drops hot plug detect on the machine's
// HDMI port for a fraction of a second, several times a minute, and
// the monitor behind it answers again with the same EDID each time.
// The kernel reports each drop as an unplug and each return as a new
// monitor. Without the hold, each reader of the card acts on the drop:
// the Display reports NoPanel and Retained, the placement pass clears
// the screen's windows from status, the slice drops the monitor's
// attributes, and media-operator's Player reports PanelAway from the
// Display. Each of them writes again when the monitor answers, so one
// drop costs several writes and several Events, and every Player and
// claim on the screen wakes for nothing.
//
// So each reader sees the card through a hold. A connector that goes
// dark answers the last reading the operator made while it was lit,
// until it has stayed dark for hotplugSettle. A connector that answers
// again inside that time ends the hold, and the readers never see the
// drop. A connector that is still dark at the end of the hold reads
// dark, and a timer at that deadline wakes a pass, so a monitor that
// really left is reported then, and not on the next unrelated event.
//
// The held reading carries Held, so a reader that acts on the wire
// itself, such as the Display controller's DDC/CI reads, leaves the
// connector alone while it is held.

import (
	"sync"
	"time"
)

// hotplugSettle is how long a connector must stay dark before the
// operator reports that its monitor left. It is more than twice the
// longest drop that plan 32 records from an A/V receiver in standby,
// and it is the window the canvas heal waits for the compositor's
// outputs to stop moving (canvasSettleWindow). A cable that somebody
// pulls out is reported after this time and the settle window of the
// slice publisher's loop.
const hotplugSettle = 5 * time.Second

// hotplugHold follows each connector across the walks of the card. One
// hold serves one reader's walk, because each reader composes its own
// reading from sysfs and the card, and the hold returns the reading
// that reader made.
type hotplugHold struct {
	// The Display controller and the placement pass call the same
	// walk from two goroutines, so every call takes the lock.
	mu  sync.Mutex
	now func() time.Time
	// expired wakes a pass at the end of a hold. A nil expired arms no
	// timer, for a reader whose own loop reads again.
	expired func()
	// lit is each connector's last reading while a monitor answered.
	lit map[string]Output
	// dark is when each held connector was first read dark, and the
	// timer that ends its hold.
	dark map[string]heldConnector
}

type heldConnector struct {
	since time.Time
	timer *time.Timer
}

func newHotplugHold(expired func()) *hotplugHold {
	return &hotplugHold{
		now:     time.Now,
		expired: expired,
		lit:     map[string]Output{},
		dark:    map[string]heldConnector{},
	}
}

// wrap puts the hold in front of one walk of the card.
func (h *hotplugHold) wrap(walk func() []Output) func() []Output {
	return func() []Output { return h.apply(walk()) }
}

// apply answers the walk with each held connector's lit reading in
// place of its dark one, and records this walk for the next.
//
// A connector this process never read lit answers dark at once:
// there is no reading to hold, and a machine that starts with its
// panel on another input must report it so.
func (h *hotplugHold) apply(outputs []Output) []Output {
	if h == nil {
		return outputs
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	held := make([]Output, len(outputs))
	for i, output := range outputs {
		held[i] = output
		connector := output.Connector
		if output.Connected {
			h.lit[connector] = output
			h.release(connector)
			continue
		}
		last, seen := h.lit[connector]
		if !seen {
			continue
		}
		dark, holding := h.dark[connector]
		if !holding {
			dark = heldConnector{since: now}
			if h.expired != nil {
				dark.timer = time.AfterFunc(hotplugSettle, h.expired)
			}
			h.dark[connector] = dark
		}
		if now.Sub(dark.since) >= hotplugSettle {
			delete(h.lit, connector)
			h.release(connector)
			continue
		}
		last.Held = true
		held[i] = last
	}
	return held
}

// release ends one connector's hold, and stops its timer, because a
// connector that answered again owes no pass at the deadline. The
// caller holds the lock.
func (h *hotplugHold) release(connector string) {
	if dark, holding := h.dark[connector]; holding {
		if dark.timer != nil {
			dark.timer.Stop()
		}
		delete(h.dark, connector)
	}
}
