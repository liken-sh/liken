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
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// monitorSettle is how long a slot's ELD must stay empty before the
// operator reports that its monitor left. It is the settle time of
// display-operator's own hold on the connector, for the reasons
// display-operator's plan 32 records, so the screen and its speakers
// report a monitor that left at the same time.
const monitorSettle = 5 * time.Second

// monitorCeiling bounds how long ELD changes keep starting a hold again,
// so a slot whose ELD keeps changing is reported within monitorCeiling
// plus monitorSettle. It is display-operator's hotplugCeiling, so the
// screen and its speakers report a monitor that left by the same bound.
const monitorCeiling = 3 * monitorSettle

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
	// started is when the process made the hold, and waiting is the
	// timer that wakes a pass when the start's settle time ends, armed
	// by the first read that is too early to publish.
	started time.Time
	waiting *time.Timer
}

type heldSlot struct {
	// first is the first empty read of this outage, and since the read
	// the hold runs from. An ELD change moves since and never first.
	first time.Time
	since time.Time
	// seen is the slot's change count at the last empty read.
	seen  uint64
	timer *time.Timer
}

func newMonitorHold(expired func()) *monitorHold {
	h := &monitorHold{
		now:     time.Now,
		expired: expired,
		lit:     map[pcmAddress]alsaEndpoint{},
		dark:    map[pcmAddress]heldSlot{},
		changes: map[pcmAddress]uint64{},
	}
	h.started = h.now()
	return h
}

// unsettled answers whether this read is too early to publish: the
// process started less than monitorSettle ago, and an HDMI slot reads
// empty that this process never read with a monitor. An operator that
// starts during a drop has no reading to hold, so its pass publishes
// nothing until the slot answers or the time passes, and the slice the
// previous operator published stays as it was.
func (h *monitorHold) unsettled(endpoints []alsaEndpoint) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	left := monitorSettle - h.now().Sub(h.started)
	if left <= 0 {
		return false
	}
	for _, endpoint := range endpoints {
		if !endpoint.HDMI || endpoint.Capture || endpoint.Monitor {
			continue
		}
		if _, seen := h.lit[pcmAddress{Card: endpoint.Card, PCM: endpoint.PCM}]; seen {
			continue
		}
		if h.waiting == nil && h.expired != nil {
			h.waiting = time.AfterFunc(left, h.expired)
		}
		return true
	}
	return false
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
		restart := holding && dark.seen != h.changes[slot] && now.Sub(dark.first) < monitorCeiling
		if !holding || restart {
			first := now
			if holding {
				first = dark.first
			}
			h.release(slot)
			dark = heldSlot{first: first, since: now, seen: h.changes[slot]}
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

// awaitMonitors reads the card, and when an HDMI slot reads empty,
// waits for an ELD change and reads again, until every slot has a
// monitor or the wait has lasted for deadline. The init container
// declares each sink's channel layout from this read before PipeWire
// starts, and a slot read in a drop would declare stereo and cost a
// PipeWire restart when the monitor answers. changes carries the
// card's control events, and the subscription opens before the first
// read, so a change during the read is not lost.
func awaitMonitors(read func() ([]alsaEndpoint, error), changes <-chan controlEvent,
	deadline time.Duration) ([]alsaEndpoint, error) {
	outputs, err := read()
	if err != nil {
		return nil, err
	}
	timeout := time.NewTimer(deadline)
	defer timeout.Stop()
	for emptySlot(outputs) {
		select {
		case <-timeout.C:
			return outputs, nil
		case event, ok := <-changes:
			if !ok {
				return outputs, nil
			}
			if event.Name != eldElementName {
				continue
			}
			if outputs, err = read(); err != nil {
				return nil, err
			}
		}
	}
	return outputs, nil
}

// emptySlot answers whether an HDMI or DisplayPort playback slot reads
// no monitor.
func emptySlot(outputs []alsaEndpoint) bool {
	for _, output := range outputs {
		if output.HDMI && !output.Capture && !output.Monitor {
			return true
		}
	}
	return false
}

// cardChanges subscribes to the control events of every delivered card
// and merges them onto one channel until ctx ends. A card whose events
// do not open adds nothing, and its slots are read once.
func cardChanges(ctx context.Context) <-chan controlEvent {
	merged := make(chan controlEvent, 16)
	for _, card := range controlCards() {
		events, err := watchControls(ctx, card)
		if err != nil {
			fmt.Fprintf(os.Stderr, "waiting for the monitors of card %d: %v\n", card, err)
			continue
		}
		go func() {
			for event := range events {
				select {
				case merged <- event:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	return merged
}
