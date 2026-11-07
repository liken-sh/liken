package main

// The hold on an HDMI or DisplayPort slot whose monitor stops answering
// for a moment.
//
// An A/V receiver in standby drops hot plug detect on the machine's
// HDMI port for a fraction of a second, several times a minute. The
// graphics driver clears the slot's ELD on each drop and writes the
// same block again when the receiver answers, and the card reports
// both changes on its control device. Without the hold, a pass that
// reads the card during a drop publishes the slot with the no-monitor
// taint, sets the Sink's Connected condition to NoMonitor, and can
// change the channel layout that the ELD gives, which restarts
// PipeWire. The next pass undoes all of it.
//
// So the pass reads the card through a hold. A slot whose ELD goes
// empty answers its last reading with a monitor, until it has stayed
// empty for monitorSettle. A slot that answers again inside that time
// ends the hold, and nothing the pass writes sees the drop. A slot that
// is still empty at the end of the hold reads empty, and a timer at
// that deadline wakes a pass, so a monitor that really left is reported
// then, and not on the next unrelated event. A change of the slot's ELD
// between two empty reads says the monitor answered and dropped again
// with no pass to read it, so the hold starts again from the later
// read.

import (
	"sync"
	"time"
)

// monitorSettle is how long a slot's ELD must stay empty before the
// operator reports that its monitor left. It is the settle time of
// display-operator's own hold on the connector, for the reasons
// display-operator's plan 32 records, so the screen and its speakers
// report a monitor that left at the same time.
const monitorSettle = 5 * time.Second

// monitorHold follows each HDMI slot across the passes. The reconcile
// pass reads through it, and the loop's wakes report each ELD change
// to it from their own goroutine, so every call takes the lock.
type monitorHold struct {
	mu  sync.Mutex
	now func() time.Time
	// expired wakes a pass at the end of a hold. A nil expired arms no
	// timer.
	expired func()
	// lit is each slot's last reading while a monitor answered.
	lit map[pcmAddress]alsaEndpoint
	// dark is when each held slot was first read empty, and the timer
	// that ends its hold.
	dark map[pcmAddress]heldSlot
	// changes counts the ELD changes the card reported on each slot.
	changes map[pcmAddress]uint64
}

type heldSlot struct {
	since time.Time
	// seen is the slot's change count at the last empty read.
	seen  uint64
	timer *time.Timer
}

func newMonitorHold(expired func()) *monitorHold {
	return &monitorHold{
		now:     time.Now,
		expired: expired,
		lit:     map[pcmAddress]alsaEndpoint{},
		dark:    map[pcmAddress]heldSlot{},
		changes: map[pcmAddress]uint64{},
	}
}

// heard records one control event. A change of a slot's ELD element
// counts for that slot, and every other event counts for nothing. A
// nil hold takes the call and records nothing.
func (h *monitorHold) heard(event controlEvent) {
	if h == nil || event.Name != eldElementName {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changes[pcmAddress{Card: event.Card, PCM: int(event.Device)}]++
}

// apply answers the card's endpoints with each held slot's lit reading
// in place of its empty one, and records this read for the next. Only
// an HDMI or DisplayPort playback slot is held, because only those
// carry an ELD. A slot this process never read with a monitor answers
// empty at once.
func (h *monitorHold) apply(endpoints []alsaEndpoint) []alsaEndpoint {
	if h == nil {
		return endpoints
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	held := make([]alsaEndpoint, len(endpoints))
	for i, endpoint := range endpoints {
		held[i] = endpoint
		if !endpoint.HDMI || endpoint.Capture {
			continue
		}
		slot := pcmAddress{Card: endpoint.Card, PCM: endpoint.PCM}
		if endpoint.Monitor {
			h.lit[slot] = endpoint
			h.release(slot)
			continue
		}
		last, seen := h.lit[slot]
		if !seen {
			continue
		}
		dark, holding := h.dark[slot]
		if !holding || dark.seen != h.changes[slot] {
			h.release(slot)
			dark = heldSlot{since: now, seen: h.changes[slot]}
			if h.expired != nil {
				dark.timer = time.AfterFunc(monitorSettle, h.expired)
			}
			h.dark[slot] = dark
		}
		if now.Sub(dark.since) >= monitorSettle {
			delete(h.lit, slot)
			h.release(slot)
			continue
		}
		held[i] = last
	}
	return held
}

// release ends one slot's hold and stops its timer, because a slot
// whose monitor answered again owes no pass at the deadline. The caller
// holds the lock.
func (h *monitorHold) release(slot pcmAddress) {
	if dark, holding := h.dark[slot]; holding {
		if dark.timer != nil {
			dark.timer.Stop()
		}
		delete(h.dark, slot)
	}
}
