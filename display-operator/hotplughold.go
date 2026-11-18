package main

// An A/V receiver in standby drops hot plug detect on the machine's
// HDMI port for a fraction of a second, several times a minute. The
// kernel reports each drop as an unplug and each return as a new
// monitor, though the EDID is the same. Without a hold, each drop sets
// the Display to NoPanel and Retained, clears the screen's windows
// from status, removes the monitor's attributes from the slice, and
// sets media-operator's Player to PanelAway. Each of those is written
// again when the monitor returns, so one drop costs several writes and
// Events and wakes every Player and claim on the screen.
//
// So the operator reads the card through a hold. While a connector
// reads disconnected, the hold returns its last connected reading,
// with Held set, until the connector has read disconnected for
// hotplugSettle. When it reads connected again before then, nothing
// downstream sees the drop. When it does not, it reads disconnected
// from then on, and a timer at the deadline wakes a pass, so the
// operator reports the missing monitor then and not on the next
// unrelated event. A drm uevent between two disconnected reads means
// the connector may have come back and dropped again between passes,
// so the hold starts again from the later read.
//
// A reader that talks to the panel, such as the Display controller
// with its DDC/CI reads, skips a connector whose reading has Held set.

import (
	"context"
	"strings"
	"sync"
	"time"
)

// hotplugSettle is how long a connector must read disconnected before
// the operator reports that its monitor left. It is longer than the
// hot plug detect drops of an A/V receiver in standby.
const hotplugSettle = 5 * time.Second

// hotplugCeiling limits how long after the first disconnected read a
// uevent can still restart a hold. A uevent that names no connector
// can come from another connector on the same card, so without a
// limit, a neighbour that drops every few seconds would hide an unplug
// here for as long as it keeps dropping. With the limit, an unplug is
// reported at most hotplugCeiling plus hotplugSettle after it.
const hotplugCeiling = 3 * hotplugSettle

// hotplugHold records when each connector started to read
// disconnected. Every hotplugView of the hold shares that record, so
// the Display controller and the slice pass report a missing monitor
// at the same deadline. Each view keeps its own connected readings,
// because each caller composes its reading of sysfs and the card in
// its own way.
type hotplugHold struct {
	// The views are read from separate goroutines.
	mu  sync.Mutex
	now func() time.Time
	// expired runs when a hold reaches its deadline, to wake a pass. A
	// nil expired arms no timer.
	expired func()
	// hotplugs returns the number of drm uevents read so far that may
	// concern the connector with this DRM object id. A nil hotplugs
	// counts none.
	hotplugs func(connectorID string) uint64
	// outages has an entry for each connector that read disconnected
	// after a connected reading, until a view reads it connected again.
	outages map[string]outage
}

type outage struct {
	// first is the time of the first disconnected read, and since is
	// the time the current hold runs from. A uevent restarts the hold
	// by moving since, and hotplugCeiling is measured from first.
	first time.Time
	since time.Time
	// hotplugsAtRead is the uevent count at the start of the current
	// hold.
	hotplugsAtRead uint64
	timer          *time.Timer
	// ended is set when the hold reaches its deadline. The connector
	// then reads disconnected to every view until a view reads it
	// connected.
	ended bool
}

func newHotplugHold(expired func(), hotplugs func(connectorID string) uint64) *hotplugHold {
	return &hotplugHold{
		now:      time.Now,
		expired:  expired,
		hotplugs: hotplugs,
		outages:  map[string]outage{},
	}
}

// newCardHold returns the operator's hold. When a hold ends, it sends
// on the same channel that retries a failed write, so a monitor that
// stayed away is reported at the deadline.
func newCardHold(retries chan<- struct{}, hotplugs func(connectorID string) uint64) *hotplugHold {
	return newHotplugHold(func() {
		select {
		case retries <- struct{}{}:
		default:
		}
	}, hotplugs)
}

// hotplugView keeps the last connected reading of each connector that
// was read through it.
type hotplugView struct {
	hold          *hotplugHold
	lastConnected map[string]Output
}

func (h *hotplugHold) view() *hotplugView {
	return &hotplugView{hold: h, lastConnected: map[string]Output{}}
}

// assumeConnected stores outputs as connected readings, as if the view
// had read them, so that the first disconnected read of each one starts a hold.
// A connector that already has a connected reading keeps it.
func (v *hotplugView) assumeConnected(outputs []Output) {
	v.hold.mu.Lock()
	defer v.hold.mu.Unlock()
	for _, output := range outputs {
		if _, read := v.lastConnected[output.Connector]; !read {
			v.lastConnected[output.Connector] = output
		}
	}
}

// rememberedScreens returns a connected reading of each disconnected
// connector whose monitor the link history remembers, for
// assumeConnected at startup. The
// reading has only the connector and the monitor's identity, because
// a reader acts on a held connector by its identity alone.
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

func (v *hotplugView) wrap(walk func() []Output) func() []Output {
	return func() []Output { return v.apply(walk()) }
}

// apply returns one read of the card with the last connected reading
// in place of each disconnected output that is held, and records the
// read for the next call. A nil view returns the outputs unchanged.
//
// A connector with no connected reading in this view, read from the card
// or stored by assumeConnected, is returned disconnected at once,
// because there is nothing to hold. A connector that reads connected is
// returned as read, so a different monitor that arrives during a hold is
// never shown as the held one.
func (v *hotplugView) apply(outputs []Output) []Output {
	if v == nil {
		return outputs
	}
	h := v.hold
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	result := make([]Output, len(outputs))
	for i, output := range outputs {
		result[i] = output
		connector := output.Connector
		if output.Connected {
			v.lastConnected[connector] = output
			h.release(connector)
			continue
		}
		last, readLit := v.lastConnected[connector]
		if !readLit {
			continue
		}
		var hotplugs uint64
		if h.hotplugs != nil {
			hotplugs = h.hotplugs(output.ConnectorID)
		}
		current, holding := h.outages[connector]
		switch {
		case !holding:
			h.start(connector, now, now, hotplugs)
		case !current.ended && current.hotplugsAtRead != hotplugs && now.Sub(current.first) < hotplugCeiling:
			h.start(connector, current.first, now, hotplugs)
		}
		current = h.outages[connector]
		if current.ended || now.Sub(current.since) >= hotplugSettle {
			current.ended = true
			h.outages[connector] = current
			delete(v.lastConnected, connector)
			continue
		}
		last.Held = true
		result[i] = last
	}
	return result
}

// start begins or restarts a connector's hold and arms the timer for
// its deadline. The caller holds the lock.
func (h *hotplugHold) start(connector string, first, now time.Time, hotplugs uint64) {
	h.release(connector)
	current := outage{first: first, since: now, hotplugsAtRead: hotplugs}
	if h.expired != nil {
		current.timer = time.AfterFunc(hotplugSettle, h.expired)
	}
	h.outages[connector] = current
}

// release ends a connector's hold and stops its timer, because a
// connector that reads connected needs no pass at the deadline. The
// caller holds the lock.
func (h *hotplugHold) release(connector string) {
	if current, holding := h.outages[connector]; holding {
		if current.timer != nil {
			current.timer.Stop()
		}
		delete(h.outages, connector)
	}
}

// hotplugCounts counts the drm uevents of the operator's card, by the
// DRM object id of the connector that each one names. An event that
// names no connector counts for every connector of the card.
//
// The uevent socket receives the events of every card on the machine,
// and DRM numbers connectors per device, so connector 403 of another
// card is not connector 403 of this one, and an event of another card
// is not counted. A report of lost events names no card, so it counts
// for every connector, because a lost event may have been this card's.
type hotplugCounts struct {
	mu       sync.Mutex
	card     string
	unnamed  uint64
	byObject map[string]uint64
}

func newHotplugCounts(card string) *hotplugCounts {
	return &hotplugCounts{card: card, byObject: map[string]uint64{}}
}

// ofCard reports whether a devpath is the card itself,
// /devices/.../drm/card1, or a device under it.
func (c *hotplugCounts) ofCard(devpath string) bool {
	return strings.HasSuffix(devpath, "/"+c.card) || strings.Contains(devpath, "/"+c.card+"/")
}

func (c *hotplugCounts) add(event drmEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if event.Action == lostEventsAction {
		c.unnamed++
		return
	}
	if !c.ofCard(event.DevPath) {
		return
	}
	if event.Connector == "" {
		c.unnamed++
		return
	}
	c.byObject[event.Connector]++
}

func (c *hotplugCounts) count(connectorID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unnamed + c.byObject[connectorID]
}

// countHotplugs counts each drm event before it passes the event on,
// so the pass that the event wakes reads a count that includes it.
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
