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
// approves a rollout. Custom resources carry only the status and scale
// subresources, so whoever may update a request may approve one, and
// splitting the two would take a second object nobody needs yet.
//
// A request ends in exactly one of two states and never retries: the
// device paired, or the window closed unapproved. The controller's own
// pairing mode has timed out by then as well, so a retry would ask the
// radio to pair with something that is no longer listening.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// reconcileRequests runs every open window aimed at this radio, and
// collects the finished requests whose time is up.
func (i *inventory) reconcileRequests(adapter *Adapter, snapshot radioSnapshot, pass *inventoryPass) {
	list, err := get[PairingRequestList](i.client, fromCache(pairingRequestsPath()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the PairingRequests: %v\n", err)
		pass.ok = false
		return
	}

	windows := 0
	for index := range list.Items {
		request := &list.Items[index]
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

	// The radio's own timeouts carry what is left of the window, so a
	// window outlives this operator by no longer than one pass. The
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
		return true
	}

	status.Seen = seenDevices(status.Seen, snapshot, now)
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
			status.Message = fmt.Sprintf("pairing with %s: %v", address, err)
			fmt.Fprintf(os.Stderr, "request %s: %s\n", name, status.Message)
			return
		}
		fmt.Printf("request %s: paired with %s\n", name, address)
	}
	// Trusting the device is what lets it reconnect on its own
	// afterwards. Without it BlueZ asks an agent to authorize each
	// service on every connection, and no agent is registered outside a
	// window.
	if err := i.radio.SetDeviceTrusted(address, true); err != nil {
		fmt.Fprintf(os.Stderr, "request %s: trusting %s: %v\n", name, address, err)
	}

	// The bond now exists, so the device's state differs from the
	// snapshot this pass read.
	device.Paired, device.Trusted = true, true
	pairing, err := i.createPairing(adapter, device, name)
	if err != nil {
		status.Message = fmt.Sprintf("recording the pairing with %s: %v", address, err)
		fmt.Fprintf(os.Stderr, "request %s: %s\n", name, status.Message)
		pass.ok = false
		return
	}
	i.writePairingStatus(pairing, adapter, address, device, true)
	pass.owners[address] = OwnerReference{
		APIVersion: pairingAPI,
		Kind:       pairingKind,
		Name:       pairing.Metadata.Name,
		UID:        pairing.Metadata.UID,
	}

	status.Phase = phasePaired
	status.Pairing = pairing.Metadata.Name
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
// Pairing of its own, and the list exists to name the devices that do
// not.
func seenDevices(seen []SeenDevice, snapshot radioSnapshot, now time.Time) []SeenDevice {
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
			break
		}
		merged = append(merged, SeenDevice{
			Address:   address,
			Name:      truncateRunes(deviceDisplayName(device), maxSeenNameBytes),
			FirstSeen: timestamp(now),
		})
		first[address] = timestamp(now)
	}
	return merged
}

// collectRequest deletes a finished request once its time is up. A
// finished request stays long enough to read the next morning. The
// Pairing's status records which request produced it, so that record
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
	due := finished.Add(ttl)
	if i.now().Before(due) {
		pass.runAgainIn(min(due.Sub(i.now()), backstopInterval))
		return
	}
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	if err := deleteObject(i.client, path); err != nil {
		fmt.Fprintf(os.Stderr, "deleting the finished request %s/%s: %v\n",
			request.Metadata.Namespace, request.Metadata.Name, err)
		pass.ok = false
		return
	}
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
// which is what everything outside a window depends on.
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
// what the object already carries.
func (i *inventory) writeRequestStatus(request *PairingRequest, status PairingRequestStatus, pass *inventoryPass) {
	if sameRequestStatus(request.Status, status) {
		return
	}
	request.Status = status
	request.APIVersion, request.Kind = pairingAPI, pairingRequestKind
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	if err := replaceStatus(i.client, path, request); err != nil {
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
		current.Pairing != next.Pairing ||
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

// watchPairingRequests wakes the loop while a request needs attention.
//
// The operator holds no informer and no watch. A request is a small
// object that a person creates by hand, and the loop's own wakes come
// from the kernel and from bluetoothd, and neither of those reports
// that somebody wrote a request or approved a device in one. So this
// lists the requests on a timer, and it wakes the loop only when the
// list contains a request that is unfinished or one whose collection
// is due. An idle cluster costs one list of one small collection every
// interval, served from the API server's watch cache.
func watchPairingRequests(ctx context.Context, client *Client, interval time.Duration, now func() time.Time) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go func() {
		defer close(wake)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			list, err := get[PairingRequestList](client, fromCache(pairingRequestsPath()))
			if err != nil {
				fmt.Fprintf(os.Stderr, "watching the PairingRequests: %v\n", err)
				continue
			}
			if !requestsNeedAPass(list.Items, now()) {
				continue
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake
}

// requestsNeedAPass reports whether any request needs the loop to run.
func requestsNeedAPass(requests []PairingRequest, now time.Time) bool {
	for _, request := range requests {
		if !request.Status.finished() {
			return true
		}
		finished := parseTimestamp(request.Status.FinishedAt)
		if finished.IsZero() || !now.Before(finished.Add(request.Spec.ttl())) {
			return true
		}
	}
	return false
}
