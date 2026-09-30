package main

// The node workload's two modes, the retries of a join that failed,
// and how the node workload follows the kernel's changes to the
// adapter's addresses.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// adapterConfig is what the spec asks of the adapter. In Control it
// also holds the OSD name the adapter announces, and the physical
// address the Display states or the reason the node workload has none.
// A change of either is a new claim of the logical address, because
// the kernel announces both from the claim.
type adapterConfig struct {
	mode     CECMode
	osdName  string
	display  string
	physical cec.PhysicalAddress
	problem  string
}

// adapterDisplay is one good read of a Display: its name and the
// physical address it stated.
type adapterDisplay struct {
	name     string
	physical cec.PhysicalAddress
}

// desired reads what the spec asks. In Control that includes a read of
// the Display, because the adapter announces the Display's physical
// address, and display-operator publishes it there from the EDID. A
// read that fails is not a new address: the node workload keeps the
// last good read of the same Display, so an API server that is briefly
// unreachable does not take the adapter off the bus, and the entry's
// message gives the read's error.
func (n *cecNode) desired(bus string, mode CECMode, display string) adapterConfig {
	n.displayNote = ""
	if mode != CECControl {
		return adapterConfig{mode: CECListen}
	}
	want := adapterConfig{mode: CECControl, osdName: osdName(bus), display: display, physical: cec.InvalidPhysicalAddress}
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

// The waits between the tries of a join that failed. A join fails when
// every playback address is taken or the kernel refuses a call, and
// both can change with no change to the spec. The wait doubles after
// each failed try up to its bound, so an adapter that cannot join does
// not claim on the wire every pass.
var (
	cecRetryFirst = 30 * time.Second
	cecRetryMax   = 10 * time.Minute
)

// nextRetry is the wait after a failed try whose wait was last.
func nextRetry(last time.Duration) time.Duration {
	if last == 0 {
		return cecRetryFirst
	}
	return min(2*last, cecRetryMax)
}

// apply brings the adapter to a configuration when it differs from the
// one the adapter runs, or when a join that failed is due for another
// try. A change of mode or of address forgets what the adapter found,
// because the adapter may now speak for another place in the tree.
// Only a failure that means the adapter left is returned; any other
// refusal becomes the entry's message.
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
// monitor. Clearing needs an initiator, so the handle takes that mode
// first. The kernel then answers nothing for the adapter, and a
// Pulse-Eight turns off its autonomous mode. The kernel allows a
// monitor only to a process with CAP_NET_ADMIN, which is the one
// capability the DaemonSet adds.
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
			return entry, fmt.Errorf("%w; the kernel allows a monitor only to a process with CAP_NET_ADMIN", err)
		}
		return entry, err
	}
	if !all {
		entry.Message = "the adapter's driver has no monitor-all mode, so the adapter hears only broadcasts"
	}
	return entry, nil
}

// osdName is the name the adapter announces: its bus's name, cut to
// the fourteen bytes CEC carries. The kernel's osd_name field is 15
// bytes with the terminating NUL. A TV lists each source by this name,
// and a room's name reads better there than a machine's. A CECBus name
// is a Kubernetes name, so it is ASCII and a byte cut splits no
// character.
func osdName(bus string) string {
	if len(bus) > 14 {
		return bus[:14]
	}
	return bus
}

// The Joining messages for a claim the kernel did not complete.
const (
	joinWaitsForAddress = "the adapter has no physical address yet; the kernel claims a logical address once it has one"
	joinFoundNoAddress  = "the kernel claimed no playback logical address: a device holds each of 4, 8, and 11"
)

// control makes the handle the adapter's only initiator and a
// follower, sets the Display's physical address, and claims a playback
// logical address. The claim clears any address a previous run left,
// because the kernel refuses a new claim on a configured adapter. A
// claim the kernel already holds with the same physical address, type,
// OSD name, and passthrough is kept: a release and a claim take the
// adapter off the bus and put it back, and the TV sees its source leave
// and return.
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
	// adapter on a video port takes its own port's address from the
	// EDID, and its driver refuses another one.
	if n.caps.Capabilities.Has(cec.CapPhysAddr) {
		if err := n.device.SetPhysicalAddress(want.physical); err != nil {
			return entry, err
		}
	}
	// Passthrough makes the kernel turn the TV remote's buttons into key
	// events on the adapter's input device as well, which a media
	// Remote claims.
	if err := n.device.Claim(cec.Claim{OSDName: want.osdName, Passthrough: true}); err != nil {
		return entry, err
	}
	return n.readClaim(entry, want)
}

// holdsClaim answers whether the kernel already holds the claim that
// control would make: one logical address of a playback device, with
// the OSD name and passthrough the claim states, on an adapter that
// announces the Display's physical address. An adapter on a video port
// takes its address from its own port, so for it the address is not
// compared.
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

// readClaim fills the entry from the adapter's addresses as the kernel
// holds them. With no physical address the kernel stores the claim and
// completes it later, and the event it sends then brings the node
// workload back here through syncAddresses.
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

// adapterChanged follows the kernel's reports about the adapter itself.
// It runs on the read loop, so it only asks the next pass to read the
// adapter's addresses; the pass owns every change to the adapter.
func (n *cecNode) adapterChanged(event cec.Event) {
	if !event.StateChange {
		return
	}
	n.mutex.Lock()
	n.resync = true
	n.mutex.Unlock()
	poke(n.wake)
}

// syncAddresses brings the entry to the addresses the kernel holds
// after a state change, in Control. The kernel can complete a stored
// claim later, when an adapter on a video port gets its address, and
// the adapter then joins with no call from the node workload. The
// kernel can also take the logical address away, and the adapter then
// joins again.
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

// adapterError answers an error that ends the node workload, and logs
// any other.
func (n *cecNode) adapterError(err error) error {
	if cec.IsGone(err) {
		return err
	}
	fmt.Fprintf(os.Stderr, "reading the adapter on %s: %v\n", n.machine, err)
	return nil
}

// scanFailure starts the message of a scan the kernel refused.
const scanFailure = "scanning the bus: "

// startMode starts the work of an adapter that joined the bus in
// Control. It scans the bus once, and after that it asks questions only
// of a device that announces itself. Nothing in Control transmits on a
// timer. A timed question is traffic on the wire, and some TVs answer
// traffic they did not expect by switching their own input, so a timer
// that asks would change the room with no person's action;
// plans/09-cec.md records the measurement. This is the node workload's
// rule for the wire: the handle took the follower mode before the
// claim, so the kernel queues every message the adapter hears from the
// moment it joins, and the scan here is the one baseline read. When the
// subscription fails, the node workload subscribes and scans again: an
// adapter that leaves ends the process, and the kubelet starts a new
// one; a claim the kernel takes away, a new physical address, and a
// change of mode each join again, which runs startMode again. The
// directory keeps the list current from what the devices
// broadcast and from the answers they send, and the TV's power follows
// the messages that change it, as cec.Directory states. stopMode waits
// for a scan or a question in progress to finish, so neither transmits
// after the handle leaves Control.
func (n *cecNode) startMode(ctx context.Context, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(ctx)
	work := &sync.WaitGroup{}
	arrivals := make(chan cec.LogicalAddress, cecArrivals)
	tvPowerAsk := make(chan struct{}, 1)
	n.stopMode = func() { cancel(); work.Wait() }
	n.modeContext, n.modeWork = ctx, work
	n.mutex.Lock()
	n.arrivals, n.introduced = arrivals, map[cec.LogicalAddress]cec.PhysicalAddress{}
	n.tvPowerAsk, n.tvPowerAskedAt = tvPowerAsk, nil
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
			case <-tvPowerAsk:
				if !n.answerTVPowerAsk(own) {
					return
				}
			}
		}
	})
}

// cecArrivals bounds the devices that wait for their questions. A bus
// has at most 14 other devices, so a full queue already holds each one.
const cecArrivals = 16

// joinScan scans the bus once when the adapter joins, and again after a
// wait only when the kernel refused the scan. The wait doubles from
// cecRetryFirst up to cecRetryMax, as a join's does. It answers false
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
			// A scan that works clears the failure of the last one, and
			// leaves any other message in place.
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

// arrived asks the mode's work to introduce the sender of a message it
// heard in Control, when the sender is new to the adapter or announced
// a new physical address. A device that joins the bus claims a logical
// address, and the kernel of that device broadcasts Report Physical
// Address then, so a device that joins or moves after the scan is heard
// here. Each device is introduced once for each physical address it
// announces, so a device that repeats its broadcasts asks nothing more
// of the adapter. The TV has one exception, in askTVPower. held says
// whether the directory held the sender before the message.
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
	n.askTVPower(message, opcode, after)
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

// introduce asks one device that arrived for the facts the directory
// does not hold yet. It answers false when the adapter left.
func (n *cecNode) introduce(own, address cec.LogicalAddress) bool {
	before := n.directory.Peers()
	_, err := cec.Introduce(n.device, n.directory, own, address, n.tvPowerReader(own))
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "asking logical address %d for its facts from the adapter on %s: %v\n", address, n.machine, err)
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

// heard is where every message the adapter receives arrives. It
// updates the directory, logs a message a person notices, and in
// Control it answers what a follower owes.
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
	// A Standby that reaches the TV is a recent command, so the next wake
	// sends Image View On without trusting a power read that can still
	// answer On.
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
	n.followRoute(message, own)
	n.answerRouting(message, after, own)
	// The adapter reports its power as on for as long as its pod runs.
	// The machine keeps running when the TV goes to standby, so a
	// Standby broadcast does not change the answer.
	reply, answers := cec.Answer(message, own, cec.PowerOn)
	if !answers {
		return
	}
	if _, err := n.device.Transmit(reply, 0, 0); err != nil {
		if cec.IsGone(err) {
			n.fail(err)
			return
		}
		fmt.Fprintf(os.Stderr, "answering %v: %v\n", message, err)
	}
}

// devicesOf turns the directory's peers into the entry's devices, with
// each fact in the words the status uses and an unknown fact absent.
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
		devices = append(devices, device)
	}
	return devices
}
