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

// hotplugCeiling bounds how long hotplug events keep starting a hold
// again. A uevent that names no connector can come from a neighbour on
// the same card, and a neighbour that drops every few seconds would
// otherwise hide a monitor that left this connector for as long as it
// drops. Past the ceiling the hold runs out its settle time, so a
// monitor that left is reported within hotplugCeiling plus
// hotplugSettle. It covers the bursts plan 32 records, which last up to
// 7 seconds.
const hotplugCeiling = 3 * hotplugSettle

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
	// hotplugs answers how many drm uevents the operator has read that
	// may concern the connector with this DRM object id. A nil hotplugs
	// counts none.
	hotplugs func(connectorID string) uint64
	// dark is when each held connector was first read dark, and the
	// timer that ends its hold.
	dark map[string]heldConnector
}

type heldConnector struct {
	// first is the first dark read of this outage, and since the read
	// the hold runs from. A hotplug event moves since and never first.
	first time.Time
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

func newHotplugHold(expired func(), hotplugs func(connectorID string) uint64) *hotplugHold {
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
func newCardHold(retries chan<- struct{}, hotplugs func(connectorID string) uint64) *hotplugHold {
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

// seed gives the view a lit reading for each output, as if it had read
// it, so the first dark read of that connector starts a hold. A
// connector the view has read already keeps its own reading.
func (v *hotplugView) seed(outputs []Output) {
	v.hold.mu.Lock()
	defer v.hold.mu.Unlock()
	for _, output := range outputs {
		if _, read := v.lit[output.Connector]; !read {
			v.lit[output.Connector] = output
		}
	}
}

// rememberedScreens answers a lit reading for each dark connector whose
// monitor the link history remembers: the connector and the monitor's
// identity, which is all a held reading needs, because a reader acts on
// a held connector's identity alone.
func rememberedScreens(outputs []Output, links *linkHistory) []Output {
	var remembered []Output
	for _, output := range outputs {
		if output.Connected {
			continue
		}
		monitor := links.remembered(output.Connector)
		if monitorID(monitor) == "" {
			continue
		}
		remembered = append(remembered, Output{
			Connector: output.Connector, ConnectorID: output.ConnectorID, Connected: true, Monitor: monitor,
		})
	}
	return remembered
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
		var hotplugs uint64
		if h.hotplugs != nil {
			hotplugs = h.hotplugs(output.ConnectorID)
		}
		dark, holding := h.dark[connector]
		switch {
		case !holding:
			h.start(connector, now, now, hotplugs)
		case !dark.over && dark.seen != hotplugs && now.Sub(dark.first) < hotplugCeiling:
			h.start(connector, dark.first, now, hotplugs)
		}
		dark = h.dark[connector]
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
func (h *hotplugHold) start(connector string, first, now time.Time, hotplugs uint64) {
	h.release(connector)
	dark := heldConnector{first: first, since: now, seen: hotplugs}
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

// hotplugCounts counts the drm uevents the operator has read: the
// ones that name no connector, which may concern any connector of the
// card, and the ones that name one connector by its DRM object id.
type hotplugCounts struct {
	mu       sync.Mutex
	any      uint64
	ofObject map[string]uint64
}

func newHotplugCounts() *hotplugCounts {
	return &hotplugCounts{ofObject: map[string]uint64{}}
}

func (c *hotplugCounts) add(event drmEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if event.Connector == "" {
		c.any++
		return
	}
	c.ofObject[event.Connector]++
}

// count answers the events that may concern one connector: every event
// that names no connector, and every one that names this one.
func (c *hotplugCounts) count(connectorID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.any + c.ofObject[connectorID]
}

// countHotplugs forwards the kernel's drm events and counts each one
// before the pass it wakes reads the card, so a hold can tell a
// connector that stayed dark from one that moved between two reads.
func countHotplugs(ctx context.Context, in <-chan drmEvent, counts *hotplugCounts) <-chan drmEvent {
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
				counts.add(event)
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
