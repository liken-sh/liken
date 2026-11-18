package main

// A PairingRequest makes the radio discoverable and pairable for a
// limited time, lists the devices the radio sees in status.seen, and
// pairs the device whose address a person writes into spec.device.
// While spec.device is empty the request pairs nothing, because
// pairing whichever device answers first could bond a stranger's
// device.
//
// Writing spec.device is the approval. A custom resource has no
// subresource for an approval, so anyone who can update a request can
// approve it.
//
// A request ends as Paired or Expired and is not reopened. By the time
// the window closes, the device's own pairing mode has also timed out.

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// reconcileRequests advances each unfinished request for this radio
// and deletes each finished request whose TTL has passed. When no
// request keeps a window open, it closes the radio's window.
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
			// The operator on the named radio serves this request.
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

// listRequests returns every PairingRequest, from the informer's store
// once it has its first list, and from the API server before then.
func (i *inventory) listRequests() ([]PairingRequest, error) {
	return informer.List[PairingRequest](i.client, i.cache.requests, pairingRequestsPath(), memo.NamespacedPath(pairingRequestPath))
}

// runWindow advances one unfinished request and reports whether its
// window is still open at the end of the pass.
func (i *inventory) runWindow(adapter *Adapter, request *PairingRequest, snapshot radioSnapshot, pass *inventoryPass) bool {
	now := i.now()
	name := request.Metadata.Namespace + "/" + request.Metadata.Name
	status := request.Status

	if status.Phase == "" {
		status.Phase = phaseOpen
		status.WindowClosesAt = timestamp(now.Add(request.Spec.window()))
		fmt.Printf("PairingRequest %s: the pairing window is open until %s\n", name, status.WindowClosesAt)
	}
	closesAt := parseTimestamp(status.WindowClosesAt)
	if closesAt.IsZero() {
		// A hand edit left status.windowClosesAt empty or unparsable.
		closesAt = now.Add(request.Spec.window())
		status.WindowClosesAt = timestamp(closesAt)
	}

	if !now.Before(closesAt) {
		// Leave the radio open. Another request may still need it, and
		// closeIdleWindow closes it at the end of the pass if none does.
		status.Phase = phaseExpired
		status.FinishedAt = timestamp(now)
		fmt.Printf("PairingRequest %s: the pairing window closed with no device approved\n", name)
		i.writeRequestStatus(request, status, false, pass)
		return false
	}

	// OpenWindow sets bluetoothd's own timeouts to the time left, so
	// bluetoothd closes the radio at the deadline even if this operator
	// stops. Every pass opens the window again, because a bluetoothd
	// that restarted or an adapter that powered back on has lost it.
	remaining := closesAt.Sub(now)
	i.windowOpen = true
	if err := i.radio.OpenWindow(remaining); err != nil {
		fmt.Fprintf(os.Stderr, "PairingRequest %s: opening the pairing window: %v\n", name, err)
		status.Message = fmt.Sprintf("The radio refused the pairing window: %v", err)
		pass.ok = false
		i.writeRequestStatus(request, status, false, pass)
		// The loop retries a failed pass only once, and no event tells
		// when the radio would accept the window, so keep trying until
		// the window's deadline.
		pass.runAgainIn(followUpDelay)
		return true
	}

	status.Seen, status.SeenTruncated = seenDevices(status.Seen, snapshot, now)
	refused := false
	if request.Spec.Device != "" {
		refused = i.approve(adapter, request, &status, snapshot, pass)
	}
	i.writeRequestStatus(request, status, refused, pass)
	pass.runAgainIn(followUpDelay)
	return !status.finished()
}

// approve pairs the device in spec.device and creates its Peripheral.
// A device that the radio has not seen yet is not in pairing mode, so
// approve leaves the window open for the next pass to try again.
//
// It returns true when bluetoothd refused the pairing. The refusal is
// in status.message, and the caller posts it as an Event.
func (i *inventory) approve(adapter *Adapter, request *PairingRequest, status *PairingRequestStatus, snapshot radioSnapshot, pass *inventoryPass) bool {
	name := request.Metadata.Namespace + "/" + request.Metadata.Name
	address, err := bonds.ParseAddress(request.Spec.Device)
	if err != nil {
		status.Message = fmt.Sprintf("spec.device %q is not a Bluetooth address", request.Spec.Device)
		return false
	}
	device, present := snapshot.device(address)
	if !present {
		status.Message = fmt.Sprintf("Waiting for %s to appear in the scan", address)
		return false
	}

	if !device.Paired {
		if err := i.radio.Pair(address); err != nil {
			i.metrics.countPairAttempt(resultRefused)
			status.Message = fmt.Sprintf("bluetoothd did not pair with %s: %v", address, err)
			fmt.Fprintf(os.Stderr, "PairingRequest %s: %s\n", name, status.Message)
			return true
		}
		i.metrics.countPairAttempt(resultPaired)
		fmt.Printf("PairingRequest %s: paired with %s\n", name, address)
	}
	// Without trust, BlueZ asks an agent to authorize each service on
	// every connection, and no agent is registered outside a window.
	if err := i.radio.SetDeviceTrusted(address, true); err != nil {
		fmt.Fprintf(os.Stderr, "PairingRequest %s: trusting %s: %v\n", name, address, err)
	}

	// The snapshot was read before the pairing.
	device.Paired, device.Bonded, device.Trusted = true, true, true
	peripheral, err := i.createPeripheral(adapter, device, name)
	if err != nil {
		status.Message = fmt.Sprintf("Paired with %s, and the Peripheral was not created: %v", address, err)
		fmt.Fprintf(os.Stderr, "PairingRequest %s: %s\n", name, status.Message)
		pass.ok = false
		return false
	}
	i.recorder.Normal(peripheralReference(peripheral), reasonPaired,
		fmt.Sprintf("Paired with %s through PairingRequest %s", address, name))
	// The kernel registers the device's battery only after the device
	// connects, and no claim can hold a Peripheral that this pass created.
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
	return false
}

// seenDevices adds the unpaired devices in the snapshot to
// status.seen. A paired device is left out because it already has a
// Peripheral.
//
// A device keeps its first firstSeen, so a person can tell which of
// two controllers answered the scan first. A busy room can hold any
// number of devices, so the list stops at maxSeenDevices and each name
// is cut to maxSeenNameBytes, the limit that a ResourceSlice sets on a
// string attribute. The second return value is true when the list was
// full and a device was left out.
func seenDevices(seen []SeenDevice, snapshot radioSnapshot, now time.Time) ([]SeenDevice, bool) {
	first := make(map[string]string, len(seen))
	for _, device := range seen {
		first[device.Address] = device.FirstSeen
	}
	// Existing entries keep their order, so the list does not shuffle
	// while a person reads it.
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
			Name:      truncateToBytes(deviceDisplayName(device), maxSeenNameBytes),
			FirstSeen: timestamp(now),
		})
		first[address] = timestamp(now)
	}
	return merged, false
}

// collectRequest deletes a finished request once its TTL has passed.
// The Peripheral's status names the request that paired it, so that
// record outlasts the request.
func (i *inventory) collectRequest(request *PairingRequest, pass *inventoryPass) {
	ttl := request.Spec.ttl()
	finished := parseTimestamp(request.Status.FinishedAt)
	if finished.IsZero() {
		// Without a recorded start, each pass would count the TTL from
		// its own time and never delete the request.
		status := request.Status
		status.FinishedAt = timestamp(i.now())
		i.writeRequestStatus(request, status, false, pass)
		return
	}
	// The request watcher wakes the loop when the TTL ends.
	if i.now().Before(finished.Add(ttl)) {
		return
	}
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	if err := deleteObject(i.client, path); err != nil {
		fmt.Fprintf(os.Stderr, "deleting PairingRequest %s/%s: %v\n",
			request.Metadata.Namespace, request.Metadata.Name, err)
		pass.ok = false
		return
	}
	i.cache.requests.Versions.Note(requestKey(*request), "")
	fmt.Printf("PairingRequest %s/%s: deleted after its time to live of %s\n",
		request.Metadata.Namespace, request.Metadata.Name, ttl)
}

// closeIdleWindow makes the radio not discoverable and not pairable,
// and stops its scan, when no request needs a window. It also closes a
// radio that reports a window this run did not open, such as one left
// open by an operator that restarted mid-window or by an adapter that
// powered back on after a USB reset.
func (i *inventory) closeIdleWindow(snapshot radioSnapshot) {
	radioOpen := snapshot.Adapter.Discoverable || snapshot.Adapter.Pairable || snapshot.Adapter.Discovering
	if !i.windowOpen && !radioOpen {
		return
	}
	if err := i.radio.CloseWindow(); err != nil {
		fmt.Fprintf(os.Stderr, "closing the pairing window of the idle radio: %v\n", err)
		return
	}
	i.windowOpen = false
	fmt.Printf("no PairingRequest needs a window, so the radio is no longer discoverable\n")
}

// writeRequestStatus writes status when it differs from the request's
// current status, and then posts its Events. refused is true when
// bluetoothd refused the pairing on this pass.
//
// The request can come from the informer's store and be older than
// the API server's copy. After a conflict, SettleStatus reads the
// request again and calls apply on the fresh copy. When the fresh copy
// still has the status this pass started from, a spec edit caused the
// conflict and the write goes ahead. When its status differs, this
// pass computed from a stale status, so it writes nothing and runs
// again. apply cannot compute the status again itself, because
// computing it pairs a device and opens the radio.
//
// A failed write posts no Event. The next pass computes the same
// change and posts it then.
func (i *inventory) writeRequestStatus(request *PairingRequest, status PairingRequestStatus, refused bool, pass *inventoryPass) {
	started := request.Status
	apply := func(latest *PairingRequest) bool {
		if !sameRequestStatus(latest.Status, started) {
			pass.runAgainIn(followUpDelay)
			return false
		}
		if sameRequestStatus(latest.Status, status) {
			return false
		}
		latest.Status = status
		latest.APIVersion, latest.Kind = pairingAPI, pairingRequestKind
		return true
	}
	path := pairingRequestPath(request.Metadata.Namespace, request.Metadata.Name)
	wrote, err := informer.SettleStatus(i.client, i.cache.requests.Versions, path, request, apply)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		// A deleted request needs no status.
		fmt.Fprintf(os.Stderr, "writing the status of %s/%s: %v\n",
			request.Metadata.Namespace, request.Metadata.Name, err)
		pass.ok = false
	}
	if wrote {
		i.postRequestEvents(request, started, status, refused)
	}
}

// postRequestEvents posts an Event for each change that one status
// write made. A refusal posts only when its message is new, because
// the window retries the pairing on every pass.
func (i *inventory) postRequestEvents(request *PairingRequest, before, after PairingRequestStatus, refused bool) {
	about := requestReference(request)
	if before.Phase == "" && after.Phase != "" {
		i.recorder.Normal(about, reasonPairingWindowOpened, fmt.Sprintf(
			"The radio %s is discoverable and pairable until %s", request.Spec.Adapter, after.WindowClosesAt))
	}
	if refused && after.Message != before.Message {
		i.recorder.Warning(about, reasonPairingRefused, after.Message)
	}
	if before.Phase == after.Phase {
		return
	}
	switch after.Phase {
	case phaseExpired:
		i.recorder.Normal(about, reasonPairingWindowExpired, fmt.Sprintf(
			"The pairing window closed at %s with no device paired", after.WindowClosesAt))
	case phasePaired:
		address, _ := bonds.ParseAddress(request.Spec.Device)
		i.recorder.Normal(about, reasonPaired, fmt.Sprintf(
			"Paired with %s and created Peripheral %s", address, after.Peripheral))
	}
}

// sameRequestStatus compares two statuses field by field, because the
// struct has a slice and == does not compile on it.
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
