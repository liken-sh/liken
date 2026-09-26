package main

// The node workload's two modes, the retries of a join that failed,
// and how the node workload follows the kernel's changes to the
// adapter's addresses.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// adapterConfig is what the spec asks of the adapter. In Control it
// also holds the physical address the Display states, or the reason
// the node workload has none.
type adapterConfig struct {
	mode     CECMode
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
func (n *cecNode) desired(mode CECMode, display string) adapterConfig {
	n.displayNote = ""
	if mode != CECControl {
		return adapterConfig{mode: CECListen}
	}
	want := adapterConfig{mode: CECControl, display: display, physical: cec.InvalidPhysicalAddress}
	if display == "" {
		want.problem = fmt.Sprintf("the CECBus names no display for machine %s", n.machine)
		return want
	}
	found, err := GetDisplay(n.client, display)
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
		n.startScans(ctx, cec.LogicalAddress(*entry.LogicalAddress))
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

// osdName is the name the adapter announces: its machine's name, cut
// to the fourteen bytes CEC carries, so each adapter on a bus sees the
// others by name.
func osdName(machine string) string {
	if len(machine) > 14 {
		return machine[:14]
	}
	return machine
}

// The Joining messages for a claim the kernel did not complete.
const (
	joinWaitsForAddress = "the adapter has no physical address yet; the kernel claims a logical address once it has one"
	joinFoundNoAddress  = "the kernel claimed no playback logical address: a device holds each of 4, 8, and 11"
)

// control makes the handle the adapter's only initiator and a
// follower, sets the Display's physical address, and claims a playback
// logical address. The claim clears any address a previous run left,
// because the kernel refuses a new claim on a configured adapter.
func (n *cecNode) control(want adapterConfig) (CECAdapterStatus, error) {
	entry := CECAdapterStatus{State: AdapterJoining}
	if err := n.device.Follow(); err != nil {
		return entry, err
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
	if err := n.device.Claim(cec.Claim{OSDName: osdName(n.machine), Passthrough: true}); err != nil {
		return entry, err
	}
	return n.readClaim(entry, want)
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
		n.startScans(ctx, held.Logical[0])
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

// startScans runs a scan now and then every cecScanInterval, until the
// mode changes. stopMode waits for a scan in progress to finish, so no
// scan transmits after the handle leaves Control.
func (n *cecNode) startScans(ctx context.Context, own cec.LogicalAddress) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	n.stopMode = func() { cancel(); <-done }
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_, err := cec.Scan(n.device, n.directory, own)
			if err != nil && cec.IsGone(err) {
				n.fail(err)
				return
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
			n.mutex.Unlock()
			n.markDirty()
			select {
			case <-ctx.Done():
			case <-time.After(cecScanInterval):
			}
		}
	}()
}

// heard is where every message the adapter receives arrives. It
// updates the directory, and in Control it answers what a follower
// owes.
func (n *cecNode) heard(message cec.Message) {
	if n.directory.Observe(message) {
		n.markDirty()
	}
	n.mutex.Lock()
	control := n.applied.mode == CECControl && n.entry.LogicalAddress != nil
	own := cec.AddressUnregistered
	if control {
		own = cec.LogicalAddress(*n.entry.LogicalAddress)
	}
	n.mutex.Unlock()
	if !control {
		return
	}
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
