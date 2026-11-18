package main

// An A/V receiver in standby drops hot plug detect on the machine's
// HDMI port for a fraction of a second, several times a minute. On each
// drop the graphics driver empties the ELD control element of the PCM
// device, and it writes the same ELD back when hot plug detect returns.
// The card sends a control event for each of the two changes. A
// reconcile pass that reads the card during the drop would publish the
// device with the no-monitor taint, set the Sink's Connected condition
// to NoMonitor, and could change the channel layout, which restarts
// PipeWire. The next pass would undo all of it.
//
// monitorHold hides these drops from the pass. When an HDMI or
// DisplayPort playback device reads with an empty ELD, apply returns
// the device's last reading with a monitor until the ELD has been empty
// for monitorSettle. If the ELD comes back sooner, the pass publishes
// nothing about the drop. A timer wakes a pass when monitorSettle ends,
// so a monitor that left is published then, and not at the next
// unrelated event. An ELD change event between two empty reads means
// that the monitor came back and dropped again between passes, so
// apply starts the monitorSettle wait again from the later read.
//
// A new operator process has no earlier reading of a device that is in
// a drop when the process starts. So the process reads the
// ResourceSlice that the previous process published, and for each
// device published with a monitor, it rebuilds the ELD from the
// device's attributes. On the first read, apply uses that ELD as the
// device's last reading with a monitor.

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

// monitorSettle is how long an ELD must stay empty before the operator
// publishes that the monitor left. It equals display-operator's
// hotplugSettle, so a monitor's screen and its speakers are reported gone
// at the same time.
// display-operator/plans/completed/32-a-short-hotplug-drop-changes-nothing.md
// gives the reasons for the value.
const monitorSettle = 5 * time.Second

// monitorCeiling limits how long after the first empty read an ELD
// change event can start the monitorSettle wait again. So a device
// whose ELD keeps changing reads empty no later than monitorCeiling
// plus monitorSettle after its first empty read. It equals
// display-operator's hotplugCeiling, for the same reason as
// monitorSettle.
const monitorCeiling = 3 * monitorSettle

// apply runs on the reconcile goroutine, and countELDChange runs on the
// goroutine that receives the cards' control events, so both take mu.
type monitorHold struct {
	mu  sync.Mutex
	now func() time.Time
	// expired wakes a reconcile pass when monitorSettle ends for a
	// device that still reads empty. When expired is nil, apply starts
	// no timer.
	expired         func()
	lastWithMonitor map[pcmAddress]alsaEndpoint
	emptyELDs       map[pcmAddress]emptyELD
	eldChanges      map[pcmAddress]uint64
	// publishedELDs is keyed by device name, because the ResourceSlice
	// names its devices and has no PCM address.
	publishedELDs map[string]eld
}

// emptyELD records a device whose ELD reads empty while apply returns
// its last reading with a monitor.
type emptyELD struct {
	firstRead time.Time
	// settleStart is when the monitorSettle wait started. It is
	// firstRead, or the later read where an ELD change started the wait
	// again.
	settleStart time.Time
	// changesAtSettleStart is the device's count in eldChanges at
	// settleStart. A different count at a later empty read means that
	// the ELD changed in between.
	changesAtSettleStart uint64
	timer                *time.Timer
}

func newMonitorHold(expired func()) *monitorHold {
	return &monitorHold{
		now:             time.Now,
		expired:         expired,
		lastWithMonitor: map[pcmAddress]alsaEndpoint{},
		emptyELDs:       map[pcmAddress]emptyELD{},
		eldChanges:      map[pcmAddress]uint64{},
		publishedELDs:   map[string]eld{},
	}
}

func (h *monitorHold) setPublishedELDs(elds map[string]eld) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, block := range elds {
		h.publishedELDs[name] = block
	}
}

func (h *monitorHold) countELDChange(event controlEvent) {
	if h == nil || event.Name != eldElementName {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.eldChanges[pcmAddress{Card: event.Card, PCM: int(event.Device)}]++
}

// For an HDMI or DisplayPort playback endpoint that reads with no ELD,
// apply returns its last reading with a monitor, until the ELD has been
// empty for monitorSettle.
func (h *monitorHold) apply(endpoints []alsaEndpoint) []alsaEndpoint {
	if h == nil {
		return endpoints
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	readings := make([]alsaEndpoint, len(endpoints))
	for i, endpoint := range endpoints {
		readings[i] = endpoint
		if !endpoint.HDMI || endpoint.Capture {
			continue
		}
		pcm := pcmAddress{Card: endpoint.Card, PCM: endpoint.PCM}
		if block, published := h.publishedELDs[endpoint.Name()]; published {
			delete(h.publishedELDs, endpoint.Name())
			if _, read := h.lastWithMonitor[pcm]; !read && !endpoint.Monitor {
				rebuilt := endpoint
				rebuilt.Monitor, rebuilt.ELD = true, block
				h.lastWithMonitor[pcm] = rebuilt
			}
		}
		if endpoint.Monitor {
			h.lastWithMonitor[pcm] = endpoint
			h.release(pcm)
			continue
		}
		last, hadMonitor := h.lastWithMonitor[pcm]
		if !hadMonitor {
			continue
		}
		empty, waiting := h.emptyELDs[pcm]
		restart := waiting && empty.changesAtSettleStart != h.eldChanges[pcm] && now.Sub(empty.firstRead) < monitorCeiling
		if !waiting || restart {
			firstRead := now
			if waiting {
				firstRead = empty.firstRead
			}
			h.release(pcm)
			empty = emptyELD{firstRead: firstRead, settleStart: now, changesAtSettleStart: h.eldChanges[pcm]}
			if h.expired != nil {
				empty.timer = time.AfterFunc(monitorSettle, h.expired)
			}
			h.emptyELDs[pcm] = empty
		}
		if now.Sub(empty.settleStart) >= monitorSettle {
			delete(h.lastWithMonitor, pcm)
			h.release(pcm)
			continue
		}
		readings[i] = last
	}
	// publishedELDs describe the card when the previous process stopped,
	// so they are valid for the first read only. A device that appears
	// later, such as one on a card added after the start, gets none.
	clear(h.publishedELDs)
	return readings
}

// release ends the device's wait and stops its timer, so that no pass
// is woken for a monitor that came back. The caller holds mu.
func (h *monitorHold) release(pcm pcmAddress) {
	if empty, waiting := h.emptyELDs[pcm]; waiting {
		if empty.timer != nil {
			empty.timer.Stop()
		}
		delete(h.emptyELDs, pcm)
	}
}

// awaitMonitors reads the card, and reads it again on each ELD change
// event while missing returns true, for at most timeout. The init
// container writes each sink's channel layout before PipeWire starts.
// A device read during a drop gets a stereo layout, and the layout
// change when the monitor comes back restarts PipeWire. The caller
// must open changes before its own first read of the card, so that an
// event during that read is not lost.
func awaitMonitors(read func() ([]alsaEndpoint, error), missing func([]alsaEndpoint) bool,
	changes <-chan controlEvent, timeout time.Duration) ([]alsaEndpoint, error) {
	outputs, err := read()
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for missing(outputs) {
		select {
		case <-timer.C:
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

// missingSinkMonitor returns a function that reports whether an HDMI
// playback endpoint reads no monitor while its Sink's status.monitor is
// set. The previous operator process wrote status.monitor from its own
// read, so the init container waits for a device that had a monitor,
// and not for the HDMI outputs of a GPU that never had one.
func missingSinkMonitor(sinks map[string]Sink) func([]alsaEndpoint) bool {
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

func sliceMonitorELDs(slice *ResourceSlice) map[string]eld {
	elds := map[string]eld{}
	if slice == nil {
		return elds
	}
	for _, device := range slice.Spec.Devices {
		if slices.ContainsFunc(device.Taints, func(taint DeviceTaint) bool { return taint.Key == noMonitorTaint }) {
			continue
		}
		if block, ok := publishedELD(device); ok {
			elds[device.Name] = block
		}
	}
	return elds
}

// publishedELD rebuilds an ELD from the attributes that
// addMonitorAttributes wrote. A device with no eldVersion attribute was
// published with no monitor, so publishedELD returns false for it.
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

func readPublishedELDs(client *apiclient.Client, nodeName string) map[string]eld {
	slice, err := apiclient.Get[ResourceSlice](client, ResourceSlicesPath+"/"+sliceName(nodeName))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the slice this node last published: %v\n", err)
		return nil
	}
	return sliceMonitorELDs(slice)
}

// readMissingSinkMonitor reads the Sinks of outputs from the API server
// and returns missingSinkMonitor for them. When NODE_NAME is unset or
// the Sinks cannot be read, the returned function always returns false,
// so the init container does not wait.
func readMissingSinkMonitor(outputs []alsaEndpoint) func([]alsaEndpoint) bool {
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
		fmt.Fprintf(os.Stderr, "declaring the layouts without waiting for a monitor: %v\n", err)
		return nothing
	}
	return func(outputs []alsaEndpoint) bool {
		named, _ := nameEndpoints(machine, outputs)
		return missingSinkMonitor(sinks)(named)
	}
}

// cardChanges subscribes to the control events of every card whose
// control device the claim gives the pod, and merges the events onto one
// channel until ctx ends. A card whose subscription fails sends no
// events, so an ELD change on it does not start a new read of the card.
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

// readOutputsForDeclaration reads the card for the init container, and
// waits up to monitorSettle for each output that reads no monitor while
// its Sink reports one. The control events stay open from before the
// first read until awaitMonitors returns, because readMissing reads the
// Sinks from the API server, which can be slow, and an ELD change
// during that read must still end the wait.
func readOutputsForDeclaration(read func() ([]alsaEndpoint, error),
	readMissing func([]alsaEndpoint) func([]alsaEndpoint) bool,
	subscribe func(context.Context) <-chan controlEvent) ([]alsaEndpoint, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := subscribe(ctx)
	outputs, err := read()
	if err != nil {
		return nil, err
	}
	return awaitMonitors(read, readMissing(outputs), changes, monitorSettle)
}
