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
// A drm uevent between two dark reads says the connector may have
// answered and dropped again with no pass to read it lit, so the hold
// starts again from the later read.
//
// The held reading carries Held, so a reader that acts on the wire
// itself, such as the Display controller's DDC/CI reads, leaves the
// connector alone while it is held.

import (
	"context"
	"sync"
	"sync/atomic"
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

// hotplugHold follows each connector across the walks of the card. It
// holds when each connector was first read dark, which every reader
// shares, so the Display, the windows, and the slice report a monitor
// that left at one deadline. Each reader reads through a view of its
// own, because each one composes its own reading from sysfs and the
// card, and a view returns the reading that reader made.
type hotplugHold struct {
	// The Display controller, the placement pass, and the slice
	// publisher read from three goroutines, so every call takes the
	// lock.
	mu  sync.Mutex
	now func() time.Time
	// expired wakes a pass at the end of a hold. A nil expired arms no
	// timer, for a reader whose own loop reads again.
	expired func()
	// hotplugs counts the drm uevents the operator has read. A nil
	// hotplugs counts none.
	hotplugs func() uint64
	// dark is when each held connector was first read dark, and the
	// timer that ends its hold.
	dark map[string]heldConnector
}

type heldConnector struct {
	since time.Time
	// seen is the hotplug count at the last dark read. A count that
	// moved since says the connector may have answered and dropped
	// again between two reads, so the hold starts again.
	seen  uint64
	timer *time.Timer
	// over says the hold ended at the deadline. The connector reads
	// dark to every view until a view reads it lit.
	over bool
}

func newHotplugHold(expired func(), hotplugs func() uint64) *hotplugHold {
	return &hotplugHold{
		now:      time.Now,
		expired:  expired,
		hotplugs: hotplugs,
		dark:     map[string]heldConnector{},
	}
}

// newCardHold is the operator's hold. The end of a hold schedules one
// more pass through the channel a failed write retries on, so a monitor
// that stayed away is reported at the deadline.
func newCardHold(retries chan<- struct{}, hotplugs func() uint64) *hotplugHold {
	return newHotplugHold(func() {
		select {
		case retries <- struct{}{}:
		default:
		}
	}, hotplugs)
}

// hotplugView is one reader's side of a hold: the last reading that
// reader made of each connector while a monitor answered.
type hotplugView struct {
	hold *hotplugHold
	lit  map[string]Output
}

func (h *hotplugHold) view() *hotplugView {
	return &hotplugView{hold: h, lit: map[string]Output{}}
}

// wrap puts the view in front of one walk of the card.
func (v *hotplugView) wrap(walk func() []Output) func() []Output {
	return func() []Output { return v.apply(walk()) }
}

// apply answers the walk with each held connector's lit reading in
// place of its dark one, and records this walk for the next.
//
// A connector this process never read lit answers dark at once:
// there is no reading to hold, and a machine that starts with its
// panel on another input must report it so. A connector that answers
// lit is read as it is, so a different monitor that arrives inside a
// hold is never shown as the one held.
func (v *hotplugView) apply(outputs []Output) []Output {
	if v == nil {
		return outputs
	}
	h := v.hold
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	var hotplugs uint64
	if h.hotplugs != nil {
		hotplugs = h.hotplugs()
	}
	held := make([]Output, len(outputs))
	for i, output := range outputs {
		held[i] = output
		connector := output.Connector
		if output.Connected {
			v.lit[connector] = output
			h.release(connector)
			continue
		}
		last, seen := v.lit[connector]
		if !seen {
			continue
		}
		dark, holding := h.dark[connector]
		if !holding || (!dark.over && dark.seen != hotplugs) {
			h.start(connector, now, hotplugs)
			dark = h.dark[connector]
		}
		if dark.over || now.Sub(dark.since) >= hotplugSettle {
			dark.over = true
			h.dark[connector] = dark
			delete(v.lit, connector)
			continue
		}
		last.Held = true
		held[i] = last
	}
	return held
}

// start begins one connector's hold at this read, and arms the timer
// that ends it. The caller holds the lock.
func (h *hotplugHold) start(connector string, now time.Time, hotplugs uint64) {
	h.release(connector)
	dark := heldConnector{since: now, seen: hotplugs}
	if h.expired != nil {
		dark.timer = time.AfterFunc(hotplugSettle, h.expired)
	}
	h.dark[connector] = dark
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

// countHotplugs forwards the kernel's drm events and counts each one
// before the pass it wakes reads the card, so a hold can tell a
// connector that stayed dark from one that moved between two reads.
func countHotplugs(ctx context.Context, in <-chan drmEvent, count *atomic.Uint64) <-chan drmEvent {
	out := make(chan drmEvent, cap(in))
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-in:
				if !ok {
					return
				}
				count.Add(1)
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
