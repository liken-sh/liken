package main

// The node workload puts the adapter in the mode of the `CECBus` spec,
// Listen or Control, retries a failed join, and follows the kernel's
// changes to the adapter's addresses.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// adapterConfig is the adapter configuration that the `CECBus` spec
// selects. In Control it also has the OSD name to announce and the
// physical address from the `Display`, or a problem that explains why
// the adapter cannot claim an address. The kernel announces the OSD
// name and the physical address when it claims the logical address, so
// a change to either needs a new claim.
type adapterConfig struct {
	mode     CECMode
	osdName  string
	display  string
	physical cec.PhysicalAddress
	problem  string
}

// adapterDisplay is the last successful read of a `Display`: its name
// and the physical address in its status.
type adapterDisplay struct {
	name     string
	physical cec.PhysicalAddress
}

// desired returns the configuration that the spec selects. In Control
// the adapter announces the physical address that `display-operator`
// reads from the EDID and writes in the `Display`'s status, so desired
// reads the `Display`. When that read fails, desired reuses the address
// of the last successful read of the same `Display`, so a short API
// server outage does not take the adapter off the bus, and the entry's
// message shows the read error.
func (n *cecNode) desired(spec CECBusSpec, display string) adapterConfig {
	n.displayNote = ""
	if spec.Mode != CECControl {
		return adapterConfig{mode: CECListen}
	}
	want := adapterConfig{mode: CECControl, osdName: osdName(spec), display: display, physical: cec.InvalidPhysicalAddress}
	if display == "" {
		want.problem = fmt.Sprintf("the CECBus names no display for machine %s", n.machine)
		return want
	}
	found, err := readDisplay(n.client, n.displays, display)
	if err != nil {
		n.retryLater()
	}
	switch {
	case err != nil && n.display.name == display:
		want.physical = n.display.physical
		n.displayNote = fmt.Sprintf("reading Display %s: %v", display, err)
	case err != nil:
		want.problem = fmt.Sprintf("reading Display %s: %v", display, err)
	case found.Status.PhysicalAddress == "":
		want.problem = fmt.Sprintf("Display %s has no status.physicalAddress", display)
	default:
		want.physical, err = cec.ParsePhysicalAddress(found.Status.PhysicalAddress)
		if err != nil {
			want.problem = fmt.Sprintf("Display %s: %v", display, err)
			break
		}
		n.display = adapterDisplay{name: display, physical: want.physical}
	}
	return want
}

// A join fails when every playback logical address is taken or the
// kernel refuses a call. Either can change without a change to the
// spec, so the node workload tries again. The wait doubles after each
// failure, from cecRetryFirst to cecRetryMax, so an adapter that cannot
// join does not send a claim on every pass.
const (
	cecRetryFirst = 30 * time.Second
	cecRetryMax   = 10 * time.Minute
)

// nextRetry returns the wait before the next try. last is the previous
// wait, or zero after the first failure.
func nextRetry(last time.Duration) time.Duration {
	if last == 0 {
		return cecRetryFirst
	}
	return min(2*last, cecRetryMax)
}

// apply configures the adapter as want when want differs from the
// applied configuration, or when a failed join is due for another try.
// A changed configuration clears the directory and the active source,
// because the devices and the source that they recorded may not apply
// to the new mode or physical address. apply returns only an error that
// means the adapter left. Any other refusal becomes the entry's
// message.
func (n *cecNode) apply(ctx context.Context, want adapterConfig) error {
	n.mutex.Lock()
	same := n.applied == want && n.entry.Machine != ""
	failed := n.entry.State == AdapterJoining || n.entry.State == AdapterRefused
	due := !n.now().Before(n.retryAt)
	n.mutex.Unlock()
	if same && !(failed && want.problem == "" && due) {
		return nil
	}
	n.stopMode()
	if !same {
		n.directory.Reset()
		n.mutex.Lock()
		n.source = cec.InvalidPhysicalAddress
		n.mutex.Unlock()
	}
	var entry CECAdapterStatus
	var err error
	if want.mode == CECControl {
		entry, err = n.control(want)
	} else {
		entry, err = n.listen()
	}
	if err != nil && cec.IsGone(err) {
		return err
	}
	if err != nil {
		entry.State = AdapterRefused
		entry.Message = err.Error()
	}
	entry.Machine, entry.Mode, entry.Driver = n.machine, want.mode, n.caps.Driver
	n.mutex.Lock()
	n.applied, n.entry = want, entry
	if entry.State == AdapterJoining || entry.State == AdapterRefused {
		n.retryWait = nextRetry(n.retryWait)
		n.retryAt = n.now().Add(n.retryWait)
	} else {
		n.retryWait = 0
	}
	n.mutex.Unlock()
	if entry.State == AdapterJoined {
		n.startMode(ctx, cec.LogicalAddress(*entry.LogicalAddress))
	}
	n.markDirty()
	return nil
}

// listen clears the adapter's logical addresses and makes the handle a
// monitor. With no logical address the kernel answers nothing for the
// adapter, and a Pulse-Eight turns off its autonomous mode. The kernel
// allows a monitor only to a process with CAP_NET_ADMIN, which
// `deploy/cec.yaml` adds to the `DaemonSet`'s pod.
func (n *cecNode) listen() (CECAdapterStatus, error) {
	entry := CECAdapterStatus{State: AdapterListening}
	if err := n.device.Initiate(); err != nil {
		return entry, err
	}
	if err := n.device.Release(); err != nil {
		return entry, err
	}
	n.directory.SetOwn(cec.AddressUnregistered)
	all := n.caps.Capabilities.Has(cec.CapMonitorAll)
	if err := n.device.Monitor(all); err != nil {
		if errors.Is(err, unix.EPERM) {
			return entry, fmt.Errorf("%w: the kernel allows a monitor only to a process with CAP_NET_ADMIN", err)
		}
		return entry, err
	}
	if !all {
		entry.Message = "the adapter's driver has no monitor-all mode, so the adapter hears only broadcasts"
	}
	return entry, nil
}

// osdName returns the name that the adapter announces and a TV shows
// for this source: `spec.osdName`, or `defaultOSDName` when the field
// is empty, as on a bus stored before the field had a default. The
// [OSD Name] operand of Set OSD Name has at most 14 characters
// (HDMI-CEC 1.3a, CEC Table 26), and the schema enforces that limit, so
// the cut here applies only to a bus that the API server did not
// validate.
func osdName(spec CECBusSpec) string {
	name := spec.OSDName
	if name == "" {
		name = defaultOSDName
	}
	if len(name) > 14 {
		return name[:14]
	}
	return name
}

// Messages for an adapter in the Joining state, when the kernel has not
// completed the claim.
const (
	joinWaitsForAddress = "the adapter has no physical address yet, and the kernel claims a logical address only once it has one"
	joinFoundNoAddress  = "the kernel claimed no playback logical address: a device holds each of 4, 8, and 11"
)

// control joins the bus as a playback device with the `Display`'s
// physical address. The kernel refuses a new claim on a configured
// adapter, so control first releases the addresses that a previous run
// left. When the kernel already has the claim that control would make,
// control keeps it, because a release and a new claim take the adapter
// off the bus and back, and the TV then shows its source leaving and
// returning.
func (n *cecNode) control(want adapterConfig) (CECAdapterStatus, error) {
	entry := CECAdapterStatus{State: AdapterJoining}
	if err := n.device.Follow(); err != nil {
		return entry, err
	}
	kept, err := n.holdsClaim(want)
	if err != nil {
		return entry, err
	}
	if kept {
		return n.readClaim(entry, want)
	}
	if err := n.device.Release(); err != nil {
		return entry, err
	}
	if want.problem != "" {
		entry.Message = want.problem
		return entry, nil
	}
	// A USB adapter has no EDID and takes the address it is given. An
	// adapter on a video port gets its address from the EDID of its
	// port, and its driver refuses another one.
	if n.caps.Capabilities.Has(cec.CapPhysAddr) {
		if err := n.device.SetPhysicalAddress(want.physical); err != nil {
			return entry, err
		}
	}
	// Passthrough makes the kernel turn the TV remote's buttons into
	// key events on the adapter's input device, which a media
	// `Remote` claims.
	if err := n.device.Claim(cec.Claim{OSDName: want.osdName, Passthrough: true}); err != nil {
		return entry, err
	}
	return n.readClaim(entry, want)
}

// holdsClaim returns whether the kernel already has the claim that
// control would make: one playback logical address, the wanted OSD name,
// passthrough, and the `Display`'s physical address. An adapter on a
// video port takes its physical address from its port, so holdsClaim
// does not compare that address.
func (n *cecNode) holdsClaim(want adapterConfig) (bool, error) {
	if want.problem != "" {
		return false, nil
	}
	held, err := n.device.Addresses()
	if err != nil {
		return false, err
	}
	if len(held.Logical) != 1 || held.Type != cec.TypePlayback || held.OSDName != want.osdName || !held.Passthrough {
		return false, nil
	}
	if !n.caps.Capabilities.Has(cec.CapPhysAddr) {
		return true, nil
	}
	actual, err := n.device.PhysicalAddress()
	if err != nil {
		return false, err
	}
	return actual == want.physical, nil
}

// readClaim fills the entry from the kernel's addresses. When the
// adapter has no physical address, the kernel stores the claim and
// completes it later, and syncAddresses calls readClaim again on the
// kernel's state-change event.
func (n *cecNode) readClaim(entry CECAdapterStatus, want adapterConfig) (CECAdapterStatus, error) {
	actual, err := n.device.PhysicalAddress()
	if err != nil {
		return entry, err
	}
	held, err := n.device.Addresses()
	if err != nil {
		return entry, err
	}
	entry.OSDName = held.OSDName
	entry.PhysicalAddress = ""
	switch {
	case actual == cec.InvalidPhysicalAddress:
		entry.Message = joinWaitsForAddress
	case actual != want.physical:
		entry.PhysicalAddress = actual.String()
		entry.Message = fmt.Sprintf("the adapter's driver sets its own physical address %s and does not take %s from Display %s", actual, want.physical, want.display)
	default:
		entry.PhysicalAddress = actual.String()
		entry.Message = ""
	}
	if len(held.Logical) == 0 {
		if actual != cec.InvalidPhysicalAddress {
			entry.Message = joinFoundNoAddress
		}
		entry.LogicalAddress = nil
		return entry, nil
	}
	own := held.Logical[0]
	logical := int(own)
	entry.LogicalAddress = &logical
	entry.State = AdapterJoined
	n.directory.SetOwn(own)
	return entry, nil
}

// adapterChanged handles the kernel's events about the adapter itself.
// It runs on the read loop, so it only requests a pass that reads the
// adapter's addresses again.
func (n *cecNode) adapterChanged(event cec.Event) {
	if !event.StateChange {
		return
	}
	n.mutex.Lock()
	n.resync = true
	n.mutex.Unlock()
	sendWake(n.wake)
}

// syncAddresses updates the entry in Control after the kernel's state
// change. The kernel can complete a stored claim with no call from the
// node workload, for example when an adapter on a video port gets its
// physical address. When the kernel removes or changes the logical
// address, syncAddresses applies the configuration again.
func (n *cecNode) syncAddresses(ctx context.Context, want adapterConfig) error {
	n.mutex.Lock()
	wanted := n.resync
	n.resync = false
	entry := n.entry
	control := n.applied.mode == CECControl && want.problem == "" && entry.State != AdapterRefused
	n.mutex.Unlock()
	if !wanted || !control {
		return nil
	}
	held, err := n.device.Addresses()
	if err != nil {
		return n.adapterError(err)
	}
	switch {
	case entry.LogicalAddress == nil && len(held.Logical) == 1:
		joinedNow, err := n.readClaim(entry, want)
		if err != nil {
			return n.adapterError(err)
		}
		n.mutex.Lock()
		n.entry, n.retryWait = joinedNow, 0
		n.mutex.Unlock()
		n.startMode(ctx, held.Logical[0])
		n.markDirty()
	case entry.LogicalAddress != nil && (len(held.Logical) != 1 || int(held.Logical[0]) != *entry.LogicalAddress):
		n.mutex.Lock()
		n.applied = adapterConfig{}
		n.mutex.Unlock()
		return n.apply(ctx, want)
	}
	return nil
}

// adapterError returns err when it means the adapter left, which ends
// the node workload, and otherwise logs err and returns nil.
func (n *cecNode) adapterError(err error) error {
	if cec.IsGone(err) {
		return err
	}
	fmt.Fprintf(os.Stderr, "reading the adapter on %s: %v\n", n.machine, err)
	return nil
}

// scanFailure starts the entry's message after a failed scan, so a
// later successful scan clears that message and no other.
const scanFailure = "scanning the bus: "

// startMode starts the work of an adapter that joined the bus in
// Control. It scans the bus once, and after that it sends a question
// only to a device that announces itself. Nothing in Control transmits
// on a timer, because some TVs switch their input when they receive
// traffic they did not expect, and that would change the TV's input
// with no action from a person.
//
// The handle became a follower before the claim, so the kernel queues
// every message the adapter receives from the moment it joins, and the
// scan is the baseline read after that subscription. After the scan the
// directory stays current from the messages that the devices send, as
// `cec.Directory` describes. Each new join runs startMode again, and
// when the adapter leaves, the process ends and the kubelet starts a
// new one, which scans again. stopMode waits for a scan or a question
// in progress, so neither transmits after the handle leaves Control.
func (n *cecNode) startMode(ctx context.Context, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(ctx)
	work := &sync.WaitGroup{}
	arrivals := make(chan cec.LogicalAddress, cecArrivals)
	tvPowerReadQueue := make(chan struct{}, 1)
	n.stopMode = func() { cancel(); work.Wait() }
	n.modeContext, n.modeWork = ctx, work
	n.mutex.Lock()
	n.arrivals, n.introduced = arrivals, map[cec.LogicalAddress]cec.PhysicalAddress{}
	n.tvPowerReadQueue, n.tvPowerQueuedAddress = tvPowerReadQueue, nil
	n.mutex.Unlock()
	work.Go(func() {
		if !n.joinScan(ctx, own) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case address := <-arrivals:
				if !n.introduce(own, address) {
					return
				}
			case <-tvPowerReadQueue:
				if !n.runQueuedTVPowerRead(own) {
					return
				}
			}
		}
	})
}

// cecArrivals is the capacity of the queue of devices that wait for an
// introduction. A bus has at most 14 other devices, so the queue has
// room for each of them.
const cecArrivals = 16

// joinScan scans the bus when the adapter joins, and retries a failed
// scan with the same doubling wait as a failed join. It returns false
// when the mode ended or the adapter left.
func (n *cecNode) joinScan(ctx context.Context, own cec.LogicalAddress) bool {
	var wait time.Duration
	for {
		_, err := cec.Scan(n.device, n.directory, own, n.tvPowerReader(own))
		if err != nil && cec.IsGone(err) {
			n.fail(err)
			return false
		}
		n.mutex.Lock()
		switch {
		case err != nil:
			n.entry.Message = scanFailure + err.Error()
		case n.entry.State == AdapterJoined || n.entry.State == AdapterScanned:
			n.entry.State = AdapterScanned
			if strings.HasPrefix(n.entry.Message, scanFailure) {
				n.entry.Message = ""
			}
		}
		if err == nil {
			for _, peer := range n.directory.Peers() {
				n.introduced[peer.Logical] = peer.Physical
			}
		}
		n.mutex.Unlock()
		n.markDirty()
		if err == nil {
			return true
		}
		wait = nextRetry(wait)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
}

// arrived queues the sender of a message for an introduction when the
// directory did not have the sender before the message (held is
// false), when the adapter has not introduced it yet, or when it
// announced a new physical address. A device that joins the bus or
// moves broadcasts Report Physical Address, so the adapter hears it
// after the scan. A device that repeats its broadcasts gets one
// introduction for each physical address. queueTVPowerRead handles questions
// about the TV's power.
func (n *cecNode) arrived(message cec.Message, after cec.Peer, held bool) {
	if message.From == cec.AddressUnregistered {
		return
	}
	opcode, _ := message.Opcode()
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.arrivals == nil {
		return
	}
	n.queueTVPowerRead(message, opcode, after)
	introduced, known := n.introduced[message.From]
	moved := opcode == cec.OpReportPhysicalAddr && after.Physical != introduced
	if held && known && !moved {
		return
	}
	n.introduced[message.From] = after.Physical
	select {
	case n.arrivals <- message.From:
	default:
	}
}

// introduce asks one device for the details that the directory does
// not have yet. It returns false when the adapter left.
func (n *cecNode) introduce(own, address cec.LogicalAddress) bool {
	before := n.directory.Peers()
	_, err := cec.Introduce(n.device, n.directory, own, address, n.tvPowerReader(own))
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the facts of logical address %d through the adapter on %s: %v\n", address, n.machine, err)
	}
	if !n.directory.Holds(address) {
		// The device left, so a later message from it is a new arrival.
		n.mutex.Lock()
		delete(n.introduced, address)
		n.mutex.Unlock()
	}
	if !slices.Equal(before, n.directory.Peers()) {
		n.markDirty()
	}
	n.noteTVPower()
	return true
}

// heard handles every message the adapter receives. It updates the
// directory, logs a message that a person would notice, and in Control
// sends the replies that the CEC specification requires of a follower.
func (n *cecNode) heard(message cec.Message) {
	held := n.directory.Holds(message.From)
	before, after, changed := n.directory.Hear(message)
	if changed {
		n.markDirty()
	}
	n.noteTVPower()
	opcode, _ := message.Opcode()
	if opcode == cec.OpActiveSource && len(message.Operands()) >= 2 {
		operands := message.Operands()
		n.noteSource(cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1]))
	}
	// A Standby to the TV, or a broadcast Standby, counts as a recent
	// command, because the TV can still report On for a while after it.
	// A power command within cecPowerSettle after it is then sent
	// without a read of the power first.
	if opcode == cec.OpStandby && !message.IsPoll() && (message.To == cec.AddressTV || message.IsBroadcast()) {
		n.mutex.Lock()
		n.lastCommand = time.Now()
		n.mutex.Unlock()
	}
	n.mutex.Lock()
	bus := n.bus
	control := n.applied.mode == CECControl && n.entry.LogicalAddress != nil
	own := cec.AddressUnregistered
	if control {
		own = cec.LogicalAddress(*n.entry.LogicalAddress)
	}
	n.mutex.Unlock()
	n.logHeard(bus, message, before, after, own)
	if !control {
		return
	}
	n.arrived(message, after, held)
	n.noteSystemAudio(message, own)
	n.followRoute(message, own)
	n.answerRouting(message, after, own)
	n.sleepOnStandby(message, after, own)
	reply, answers := cec.Answer(message, own, n.ownPower(), n.directory.Holds(cec.AddressAudioSystem))
	if !answers {
		return
	}
	if _, err := n.device.Transmit(reply, 0, 0); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "replying to %v: %v\n", message, err)
	}
}

// devicesOf converts the directory's peers to the entry's devices. A
// field that the directory has no value for is left empty.
func devicesOf(peers []cec.Peer) []CECDevice {
	devices := make([]CECDevice, 0, len(peers))
	for _, peer := range peers {
		device := CECDevice{
			LogicalAddress: int(peer.Logical),
			Type:           string(peer.Type),
			OSDName:        peer.OSDName,
			Vendor:         peer.Vendor.String(),
			CECVersion:     peer.Version.String(),
			Power:          peer.Power.String(),
		}
		if peer.Physical != cec.InvalidPhysicalAddress {
			device.PhysicalAddress = peer.Physical.String()
		}
		if peer.SystemAudioMode != cec.SystemAudioUnknown {
			device.SystemAudioMode = peer.SystemAudioMode.String()
			device.SystemAudioModeAt = timestamp(peer.SystemAudioModeAt)
		}
		devices = append(devices, device)
	}
	return devices
}
