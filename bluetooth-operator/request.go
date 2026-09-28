package main

// The PairingRequest: the act of pairing, and its discovery scan.
//
// The scan and the pairing window are one radio session, so the
// address a person approves is one the radio observed in this same
// session. A request opens that session, reports the radio's
// observations in status.seen, and pairs exactly the device whose
// address somebody wrote into spec.device. An empty spec.device never
// pairs anything: pairing whatever responds first is the one behavior
// that can bond a stranger's device.
//
// Writing the spec is the approval, the same way editing a Deployment
// approves a rollout. Custom resources have only the status and scale
// subresources, so whoever may update a request may approve one, and
// splitting the two would take a second object nobody needs yet.
//
// A request ends in exactly one of two states and never retries: the
// device paired, or the window closed unapproved. The controller's own
// pairing mode has timed out by then as well, so a retry would ask the
// radio to pair with something that is no longer listening.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// reconcileRequests runs every open window aimed at this radio, and
// collects the finished requests whose time is up.
func (i *inventory) reconcileRequests(adapter *Adapter, snapshot radioSnapshot, pass *inventoryPass) {
	requests, err := i.listRequests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the PairingRequests: %v\n", err)
		pass.ok = false
		return
	}

	windows := 0
	for index := range requests {
		request := &requests[index]
		if request.Spec.Adapter != adapter.Metadata.Name || request.Metadata.deleting() {
			// Another radio's request. The operator that holds that radio
			// serves it, and this one must not open a window on a
			// person's behalf against hardware they did not name.
			continue
		}
		if request.Status.finished() {
			i.collectRequest(request, pass)
			continue
		}
		if i.runWindow(adapter, request, snapshot, pass) {
			windows++
		}
	}

	if windows == 0 {
		i.closeIdleWindow(snapshot)
	}
}

// listRequests answers every PairingRequest, from the store once it
// holds its first read.
func (i *inventory) listRequests() ([]PairingRequest, error) {
	if i.cache.requests.view.ready() {
		return currentList[PairingRequest](i.client, i.cache.requests, func(key string) string {
			namespace, name, _ := strings.Cut(key, "/")
			return pairingRequestPath(namespace, name)
		})
	}
	list, err := get[PairingRequestList](i.client, fromCache(pairingRequestsPath()))
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// runWindow advances one unfinished request, and reports whether its
// window is still open at the end of the pass.
func (i *inventory) runWindow(adapter *Adapter, request *PairingRequest, snapshot radioSnapshot, pass *inventoryPass) bool {
	now := i.now()
	name := request.Metadata.Namespace + "/" + request.Metadata.Name
	status := request.Status

	if status.Phase == "" {
		status.Phase = phaseOpen
		status.WindowClosesAt = timestamp(now.Add(request.Spec.window()))
		fmt.Printf("request %s: the window is open until %s\n", name, status.WindowClosesAt)
	}
	closesAt := parseTimestamp(status.WindowClosesAt)
	if closesAt.IsZero() {
		// A status somebody edited by hand, or a write that failed
		// halfway. The window gets its full length from now, which is the
		// same thing the first pass would have given it.
		closesAt = now.Add(request.Spec.window())
		status.WindowClosesAt = timestamp(closesAt)
	}

	if !now.Before(closesAt) {
		// The radio is put back by closeIdleWindow at the end of the
		// pass, not here, because another request may still be holding a
		// window open on the same radio.
		status.Phase = phaseExpired
		status.FinishedAt = timestamp(now)
		fmt.Printf("request %s: the window closed with no approval\n", name)
		i.writeRequestStatus(request, status, pass)
		return false
	}

	// The radio's own timeouts are set to what is left of the window, so
	// a window outlives this operator by no longer than one pass. The
	// window is re-asserted on every pass because a bluetoothd that
	// restarted, or an adapter that powered itself back on, holds none
	// of this state.
	remaining := closesAt.Sub(now)
	i.windowOpen = true
	if err := i.radio.OpenWindow(remaining); err != nil {
		fmt.Fprintf(os.Stderr, "request %s: opening the window: %v\n", name, err)
		status.Message = fmt.Sprintf("the radio refused the window: %v", err)
		pass.ok = false
		i.writeRequestStatus(request, status, pass)
		// The loop retries a failed pass once, and no event reports that
		// the radio would now accept the window. The follow-up pass asks
		// again while the window lasts.
		pass.runAgainIn(followUpDelay)
		return true
	}

	status.Seen, status.SeenTruncated = seenDevices(status.Seen, snapshot, now)
	if request.Spec.Device != "" {
		i.approve(adapter, request, &status, snapshot, pass)
	}
	i.writeRequestStatus(request, status, pass)
	pass.runAgainIn(followUpDelay)
	return !status.finished()
}

// approve pairs the one device a person named.
//
// A device that is not in the snapshot is one the radio has not
// observed yet, which is the ordinary state before somebody holds the
// controller's buttons. The window stays open and the next pass looks
// again, until the window closes on its own.
func (i *inventory) approve(adapter *Adapter, request *PairingRequest, status *PairingRequestStatus, snapshot radioSnapshot, pass *inventoryPass) {
	name := request.Metadata.Namespace + "/" + request.Metadata.Name
	address, err := bonds.ParseAddress(request.Spec.Device)
	if err != nil {
		status.Message = fmt.Sprintf("spec.device %q is not a Bluetooth address", request.Spec.Device)
		return
	}
	device, present := snapshot.device(address)
	if !present {
		status.Message = fmt.Sprintf("waiting for %s to answer the scan", address)
		return
	}

	if !device.Paired {
		if err := i.radio.Pair(address); err != nil {
			i.metrics.countPairAttempt(resultRefused)
			status.Message = fmt.Sprintf("pairing with %s: %v", address, err)
			fmt.Fprintf(os.Stderr, "request %s: %s\n", name, status.Message)
			return
		}
		i.metrics.countPairAttempt(resultPaired)
		fmt.Printf("request %s: paired with %s\n", name, address)
	}
	// Trusting the device lets it reconnect on its own afterwards.
	// Without it BlueZ asks an agent to authorize each service on every
	// connection, and no agent is registered outside a window.
	if err := i.radio.SetDeviceTrusted(address, true); err != nil {
		fmt.Fprintf(os.Stderr, "request %s: trusting %s: %v\n", name, address, err)
	}

	// The bond now exists, so the device's state differs from the
	// snapshot this pass read.
	device.Paired, device.Bonded, device.Trusted = true, true, true
	peripheral, err := i.createPeripheral(adapter, device, name)
	if err != nil {
		status.Message = fmt.Sprintf("recording the pairing with %s: %v", address, err)
		fmt.Fprintf(os.Stderr, "request %s: %s\n", name, status.Message)
		pass.ok = false
		return
	}
	// The device paired moments ago, and the kernel registers a power
	// supply only after it connects, so there is no kernel reading to
	// pass. It also holds no prepared claim yet: the pod that would
	// hold one cannot exist before this Peripheral does.
	i.writePeripheralStatus(peripheral, adapter, address, device, true, nil, false)
	pass.owners[address] = OwnerReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       peripheral.Metadata.Name,
		UID:        peripheral.Metadata.UID,
	}

	status.Phase = phasePaired
	status.Peripheral = peripheral.Metadata.Name
	status.FinishedAt = timestamp(i.now())
	status.Message = ""
}

// seenDevices merges the radio's current observations into the list a
// person reads.
//
// A device keeps the firstSeen it was given, because that value
// records which of two controllers started responding first. The list
// is capped and the names are cut, because it is written from radio
// observations: a busy room would otherwise grow the object without
// limit, and the limits are the same ones a ResourceSlice puts on a
// string attribute.
//
// A device the radio already holds a bond with is left out. It has a
// Peripheral of its own, and the list exists to name the devices that
// do not.
//
// The second value is true when the cap stopped a device from reaching
// the list. status.seenTruncated reports it, so the devices a busy room
// kept off the list are visible on the request.
func seenDevices(seen []SeenDevice, snapshot radioSnapshot, now time.Time) ([]SeenDevice, bool) {
	first := make(map[string]string, len(seen))
	for _, device := range seen {
		first[device.Address] = device.FirstSeen
	}
	// The devices already in the list keep their order and their place,
	// so an entry keeps its position while a person reads the list.
	merged := append([]SeenDevice{}, seen...)
	for _, device := range snapshot.Devices {
		if device.Paired {
			continue
		}
		address := device.Address.Directory()
		if _, found := first[address]; found {
			continue
		}
		if len(merged) >= maxSeenDevices {
			return merged, true
		}
		merged = append(merged, SeenDevice{
			Address:   address,
			Name:      truncateRunes(deviceDisplayName(device), maxSeenNameBytes),
			FirstSeen: timestamp(now),
		})
		first[address] = timestamp(now)
	}
	return merged, false
}

// collectRequest deletes a finished request once its time is up. A
// finished request stays long enough to read the next morning. The
// Peripheral's status records which request produced it, so that record
// outlasts the deletion.
func (i *inventory) collectRequest(request *PairingRequest, pass *inventoryPass) {
	ttl := request.Spec.ttl()
	finished := parseTimestamp(request.Status.FinishedAt)
	if finished.IsZero() {
		// A request that reached an end state with no time on it, which
		// is a status somebody edited or a write that failed halfway.
		// The time is written now, so the TTL has a start: without one,
		// every pass would count from itself and the request would never
		// be collected.
		status := request.Status
		status.FinishedAt = timestamp(i.now())
		i.writeRequestStatus(request, status, pass)
		return
	}
	// A request inside its TTL needs no follow-up pass. The request
	// watcher sets a clock for the moment the TTL is up, and wakes the
	// loop then.
	if i.now().Before(finished.Add(ttl)) {
		return
	}
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	if err := deleteObject(i.client, path); err != nil {
		fmt.Fprintf(os.Stderr, "deleting the finished request %s/%s: %v\n",
			request.Metadata.Namespace, request.Metadata.Name, err)
		pass.ok = false
		return
	}
	i.cache.requests.versions.note(requestKey(*request), "")
	fmt.Printf("request %s/%s: collected %s after it finished\n",
		request.Metadata.Namespace, request.Metadata.Name, ttl)
}

// closeIdleWindow returns the radio to idle when no request is
// holding a window open.
//
// It runs on the two states that mean a window is open: one this
// operator opened, which is the pass where a request paired or
// expired, and one the radio reports, which covers an operator that
// was restarted mid-window and an adapter that came back powered from
// a USB reset. An idle adapter is not discoverable and not pairable,
// and everything outside a window depends on that.
//
// bluetoothd's own timeouts end an open window as well, so this is
// the second of two protections against the same exposure and not the
// only one.
func (i *inventory) closeIdleWindow(snapshot radioSnapshot) {
	standing := snapshot.Adapter.Discoverable || snapshot.Adapter.Pairable || snapshot.Adapter.Discovering
	if !i.windowOpen && !standing {
		return
	}
	if err := i.radio.CloseWindow(); err != nil {
		fmt.Fprintf(os.Stderr, "closing the window on an idle radio: %v\n", err)
		return
	}
	i.windowOpen = false
	fmt.Printf("request: no window is open; the radio is no longer discoverable\n")
}

// writeRequestStatus writes a request's status when it differs from
// what the object already has.
//
// A copy from the store can be older than the API server's. The write
// from it is refused, and the request is read again. When the fresh
// copy holds the status this pass composed from, a spec edit made the
// conflict, and the status is written onto the fresh copy. When the
// fresh copy holds a newer status, that status is this operator's own
// last write, and this pass composed from an older one: the pass
// writes nothing, and the follow-up pass composes again from the newer
// status. A window's status is not a pure function of the object, so it
// cannot be composed again here: the pass pairs a device and opens the
// radio's window while it composes.
func (i *inventory) writeRequestStatus(request *PairingRequest, status PairingRequestStatus, pass *inventoryPass) {
	published := request.Status
	apply := func(held *PairingRequest) bool {
		if !sameRequestStatus(held.Status, published) {
			pass.runAgainIn(followUpDelay)
			return false
		}
		if sameRequestStatus(held.Status, status) {
			return false
		}
		held.Status = status
		held.APIVersion, held.Kind = pairingAPI, pairingRequestKind
		return true
	}
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	if _, err := settleStatus(i.client, i.cache.requests.versions, path, request, apply); err != nil && !errors.Is(err, ErrNotFound) {
		// A request somebody deleted needs no status.
		fmt.Fprintf(os.Stderr, "writing the status of %s/%s: %v\n",
			request.Metadata.Namespace, request.Metadata.Name, err)
		pass.ok = false
	}
}

// sameRequestStatus compares two statuses. The seen list is a slice, so
// the comparison walks it rather than using an equality operator.
func sameRequestStatus(current, next PairingRequestStatus) bool {
	if current.Phase != next.Phase ||
		current.WindowClosesAt != next.WindowClosesAt ||
		current.SeenTruncated != next.SeenTruncated ||
		current.Peripheral != next.Peripheral ||
		current.FinishedAt != next.FinishedAt ||
		current.Message != next.Message ||
		len(current.Seen) != len(next.Seen) {
		return false
	}
	for index, device := range current.Seen {
		if device != next.Seen[index] {
			return false
		}
	}
	return true
}
