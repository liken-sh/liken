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
//
// An operator that starts while a slot is in a drop has read no monitor
// on it. The slice the previous operator published names each slot it
// published with a monitor, with every fact of the ELD it read, so the
// hold starts from those readings (seed), and the first empty read of
// such a slot starts a hold like any other drop.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
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
	// seeds is the ELD the previous operator published for each slot,
	// by device name, until this process reads the slot.
	seeds map[string]eld
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
	return &monitorHold{
		now:     time.Now,
		expired: expired,
		lit:     map[pcmAddress]alsaEndpoint{},
		dark:    map[pcmAddress]heldSlot{},
		changes: map[pcmAddress]uint64{},
		seeds:   map[string]eld{},
	}
}

// seed gives the hold the ELD the previous operator published for each
// slot, by device name. A slot this process reads takes its own reading
// in place of the seed.
func (h *monitorHold) seed(seeds map[string]eld) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, block := range seeds {
		h.seeds[name] = block
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
		if block, seeded := h.seeds[endpoint.Name()]; seeded {
			delete(h.seeds, endpoint.Name())
			if _, read := h.lit[slot]; !read && !endpoint.Monitor {
				lit := endpoint
				lit.Monitor, lit.ELD = true, block
				h.lit[slot] = lit
			}
		}
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
	// A seed describes the card as the previous operator left it, so it
	// serves this first read alone. A slot this read did not hold, such
	// as one on a card that arrives later, is read as it is.
	clear(h.seeds)
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

// awaitMonitors reads the card, and while waitFor answers that a slot
// it expects a monitor on reads empty, waits for an ELD change and reads
// again, until the wait has lasted for deadline. The init container
// declares each sink's channel layout from this read before PipeWire
// starts, and a slot read in a drop would declare stereo and cost a
// PipeWire restart when the monitor answers. changes carries the
// card's control events, and the subscription opens before the first
// read, so a change during the read is not lost.
func awaitMonitors(read func() ([]alsaEndpoint, error), waitFor func([]alsaEndpoint) bool,
	changes <-chan controlEvent, deadline time.Duration) ([]alsaEndpoint, error) {
	outputs, err := read()
	if err != nil {
		return nil, err
	}
	timeout := time.NewTimer(deadline)
	defer timeout.Stop()
	for waitFor(outputs) {
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

// expectedEmpty answers, for a read of the card, whether a playback
// slot reads no monitor while its Sink reports one. The Sink's
// status.monitor is what the previous operator read on the slot, so
// only a slot that had a monitor is waited for, and a GPU's HDMI slots
// that never had one are not.
func expectedEmpty(sinks map[string]Sink) func([]alsaEndpoint) bool {
	return func(outputs []alsaEndpoint) bool {
		for _, output := range outputs {
			if !output.HDMI || output.Capture || output.Monitor {
				continue
			}
			if sink, ok := sinks[output.Name()]; ok && sink.Status.Monitor != nil {
				return true
			}
		}
		return false
	}
}

// monitorSeeds answers the ELD of each device the slice published with
// a monitor, by device name: a device that carries the ELD's facts and
// no no-monitor taint.
func monitorSeeds(slice *ResourceSlice) map[string]eld {
	seeds := map[string]eld{}
	if slice == nil {
		return seeds
	}
	for _, device := range slice.Spec.Devices {
		if slices.ContainsFunc(device.Taints, func(taint DeviceTaint) bool { return taint.Key == noMonitorTaint }) {
			continue
		}
		if block, ok := publishedELD(device); ok {
			seeds[device.Name] = block
		}
	}
	return seeds
}

// publishedELD rebuilds the ELD a device was published from, out of the
// attributes addMonitorAttributes wrote. A device with no eldVersion
// was published with no monitor.
func publishedELD(device SliceDevice) (eld, bool) {
	attributes := device.Attributes
	version, ok := attributes["eldVersion"]
	if !ok || version.Int == nil {
		return eld{}, false
	}
	text := func(key string) string {
		if attribute, ok := attributes[key]; ok && attribute.String != nil {
			return *attribute.String
		}
		return ""
	}
	number := func(key string) int64 {
		if attribute, ok := attributes[key]; ok && attribute.Int != nil {
			return *attribute.Int
		}
		return 0
	}
	product, err := strconv.ParseUint(strings.TrimPrefix(text("product"), "0x"), 16, 16)
	if err != nil {
		return eld{}, false
	}
	block := eld{
		Version:        byte(*version.Int),
		ConnectionType: text("connectionType"),
		Manufacturer:   text("manufacturer"),
		Product:        uint16(product),
		MonitorName:    text("monitorName"),
		LPCMChannels:   int(number("lpcmChannels")),
		LPCMMaxRateHz:  int(number("lpcmMaxRateHz")),
		Speakers:       text("speakers"),
		PortID:         uint64(number("portID")),
	}
	for bit, name := range eldSpeakerNames {
		if slices.Contains(strings.Fields(block.Speakers), name) {
			block.SpeakerAllocation |= 1 << bit
		}
	}
	for bit, depth := range eldBitDepths {
		if slices.Contains(strings.Fields(text("lpcmBitDepths")), strconv.Itoa(depth)) {
			block.LPCMDepths |= 1 << bit
		}
	}
	block.EncodedTypes = encodedTypes(strings.Fields(text(encodedFormatsAttribute)))
	return block, true
}

// readPublishedSeeds reads the slice the previous operator published,
// and answers the seeds of its devices. A slice that does not read
// seeds nothing, which is the behavior of an operator with no seed.
func readPublishedSeeds(client *apiclient.Client, nodeName string) map[string]eld {
	slice, err := apiclient.Get[ResourceSlice](client, ResourceSlicesPath+"/"+sliceName(nodeName))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the slice this node last published: %v\n", err)
		return nil
	}
	return monitorSeeds(slice)
}

// declaredMonitors reads the Sinks of the card's outputs for the init
// container, and answers which empty slots it waits for. A Sink that
// does not read waits for nothing.
func declaredMonitors(outputs []alsaEndpoint) func([]alsaEndpoint) bool {
	nothing := func([]alsaEndpoint) bool { return false }
	machine := os.Getenv("NODE_NAME")
	if machine == "" {
		return nothing
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: declareAPITimeout})
	if err != nil {
		return nothing
	}
	named, _ := nameEndpoints(machine, outputs)
	sinks, err := readSinks(named, func(name string) (*Sink, error) {
		return getSink(client, name)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v; declaring the layouts without waiting for a monitor\n", err)
		return nothing
	}
	return func(outputs []alsaEndpoint) bool {
		named, _ := nameEndpoints(machine, outputs)
		return expectedEmpty(sinks)(named)
	}
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

// declaredReading reads the card for the init container: it opens the
// cards' events, reads the card, asks expect which empty slots to wait
// for, and waits for them. The events stay open until the wait returns,
// so a wait that starts after a slow read of the Sinks still hears an
// ELD change at its end.
func declaredReading(read func() ([]alsaEndpoint, error),
	expect func([]alsaEndpoint) func([]alsaEndpoint) bool,
	subscribe func(context.Context) <-chan controlEvent) ([]alsaEndpoint, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := subscribe(ctx)
	outputs, err := read()
	if err != nil {
		return nil, err
	}
	return awaitMonitors(read, expect(outputs), changes, monitorSettle)
}
