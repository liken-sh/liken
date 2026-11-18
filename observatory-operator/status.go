package main

// The status writer writes the status of every resource except a
// Reservation, whose runner writes its own. INDI updates arrive several
// times a second while a mount slews or a camera cools. To keep that
// from becoming API writes, the writer composes every status at most
// once per statusWindow and writes only the statuses that changed, so
// the API server receives at most one write per object per window.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/drivers"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// statusWindow is the minimum time between two passes of the status
// writer.
const statusWindow = time.Second

// writeStatuses runs the status writer until ctx ends. Each pass
// composes and writes the statuses, waits for a change in the watched
// objects, and then waits out statusWindow.
func (o *operator) writeStatuses(ctx context.Context) {
	seen := &statusMemo{}
	first := newFirstRead()
	for {
		wake := o.changed.wait()
		var holdEnd, expiry time.Time
		if o.stores.ready() && o.loaded.Load() {
			t := o.snapshot()
			if holdEnd = first.hold(ctx, o, t); holdEnd.IsZero() {
				expiry = o.writeAll(ctx, t, seen)
			}
		}
		// No watch event announces the end of the hold on first reads
		// (firstread.go) or the expiry of a SetUp (setup.go), so the
		// writer sets a timer for each.
		end, stopEnd := clock(holdEnd)
		expires, stopExpires := clock(expiry)
		// No watch event follows a failed write, so after one the writer
		// skips the wait for a change and composes again after
		// statusWindow.
		if !seen.failed {
			select {
			case <-ctx.Done():
			case <-wake:
			case <-end:
			case <-expires:
			}
		}
		stopEnd()
		stopExpires()
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(statusWindow)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// clock returns a channel that receives at the given time, and a
// function that stops its timer. For a zero time it returns a nil
// channel, which a select never receives from.
func clock(at time.Time) (<-chan time.Time, func()) {
	if at.IsZero() {
		return nil, func() {}
	}
	timer := time.NewTimer(time.Until(at))
	return timer.C, func() { timer.Stop() }
}

// writeAll composes and writes the status of every resource except the
// reservations, and updates the namespace's series from what it
// composed (series.go). It records the pass in the metrics for each
// kind (metrics.go). It returns the time when the earliest SetUp
// expires, or zero when no SetUp is active.
func (o *operator) writeAll(ctx context.Context, t *tree, seen *statusMemo) time.Time {
	client := o.client.WithContext(ctx)
	seen.begin()
	times := newPassTimes()
	defer o.series.m.observePasses(times)
	var expiry time.Time
	composed := map[string]deviceStatus{}
	for _, d := range t.devices {
		began := time.Now()
		next, reading := o.deviceStatus(t, d)
		o.series.device(d, reading)
		composed[d.key()] = next
		object := d.object
		place := placeOf(t, d.kind, d.name())
		expiry = nextExpiry(expiry, object.Status.Conditions, object.Status.SetUpUntil, place)
		failed := writeStatus(o.recorder, client, seen, d.kind, &object, reference(d.kind, object.Metadata),
			func(current *deviceObject) any { return current.Status },
			func(current *deviceObject) (any, []observatory.Condition) {
				composed := next
				var transitions []observatory.Condition
				composed.Conditions, transitions = mergeConditions(current.Status.Conditions, next.Conditions)
				composed.SetUpUntil = current.Status.SetUpUntil
				if expired := expireSetUp(&composed.Conditions, composed.SetUpUntil, place); expired != nil {
					transitions = append(transitions, *expired)
				}
				composed.SetUpExpiryTime = expiryTime(composed.Conditions, composed.SetUpUntil, place)
				current.APIVersion, current.Kind, current.Status = observatory.APIVersion, d.kind.Name, composed
				return composed, transitions
			})
		times.add(d.kind, began, failed)
	}
	// A telescope's status includes its guider's phase and state, so
	// the guiders' statuses are composed before the telescopes'.
	guiders := map[string]observatory.GuiderStatus{}
	for name, guider := range t.guiders {
		began := time.Now()
		guiders[name] = o.guiderStatus(t, guider)
		o.series.guider(name, o.guiderReading(guider, guiders[name]))
		times.add(observatory.GuiderKind, began, false)
	}
	for _, telescope := range t.telescopes {
		began := time.Now()
		next := o.telescopeStatus(t, telescope, composed, guiders)
		o.series.telescope(telescope.Metadata.Name, next.Phase)
		failed := writeTyped(o.recorder, client, seen, observatory.TelescopeKind, telescope, next, statusParts[observatory.TelescopeStatus]{
			conditions: func(s *observatory.TelescopeStatus) *[]observatory.Condition { return &s.Conditions },
			until:      func(s *observatory.TelescopeStatus) **time.Time { return &s.SetUpUntil },
			expires:    func(s *observatory.TelescopeStatus) **time.Time { return &s.SetUpExpiryTime },
			place:      placeOf(t, observatory.TelescopeKind, telescope.Metadata.Name),
		}, &expiry)
		times.add(observatory.TelescopeKind, began, failed)
	}
	for _, site := range t.observatories {
		began := time.Now()
		next := o.observatoryStatus(t, site, composed)
		failed := writeTyped(o.recorder, client, seen, observatory.ObservatoryKind, site, next, statusParts[observatory.ObservatoryStatus]{
			conditions: func(s *observatory.ObservatoryStatus) *[]observatory.Condition { return &s.Conditions },
			until:      func(s *observatory.ObservatoryStatus) **time.Time { return &s.SetUpUntil },
			expires:    func(s *observatory.ObservatoryStatus) **time.Time { return &s.SetUpExpiryTime },
			place:      &site.Spec.Location,
		}, &expiry)
		times.add(observatory.ObservatoryKind, began, failed)
	}
	for _, train := range t.trains {
		began := time.Now()
		next := trainStatus(t, train, composed)
		failed := writeTyped(o.recorder, client, seen, observatory.OpticalTrainKind, train, next, statusParts[observatory.OpticalTrainStatus]{
			conditions: func(s *observatory.OpticalTrainStatus) *[]observatory.Condition { return &s.Conditions },
			place:      placeOf(t, observatory.OpticalTrainKind, train.Metadata.Name),
		}, &expiry)
		times.add(observatory.OpticalTrainKind, began, failed)
	}
	for _, tube := range t.tubes {
		began := time.Now()
		next := tubeStatus(t, tube)
		failed := writeTyped(o.recorder, client, seen, observatory.OpticalTubeKind, tube, next, statusParts[observatory.OpticalTubeStatus]{
			conditions: func(s *observatory.OpticalTubeStatus) *[]observatory.Condition { return &s.Conditions },
			place:      placeOf(t, observatory.OpticalTubeKind, tube.Metadata.Name),
		}, &expiry)
		times.add(observatory.OpticalTubeKind, began, failed)
	}
	for name, guider := range t.guiders {
		began := time.Now()
		failed := writeTyped(o.recorder, client, seen, observatory.GuiderKind, guider, guiders[name], statusParts[observatory.GuiderStatus]{
			conditions: func(s *observatory.GuiderStatus) *[]observatory.Condition { return &s.Conditions },
			place:      placeOf(t, observatory.GuiderKind, guider.Metadata.Name),
		}, &expiry)
		times.add(observatory.GuiderKind, began, failed)
	}
	o.series.endPass(t.hasSeries)
	return expiry
}

// statusParts selects the fields of one kind's status that the writer
// merges with the stored status. until is the setUpUntil that a person
// writes and expires is the setUpExpiryTime that the operator writes.
// Both are nil for a kind that has no SetUp. place is the location of
// the resource's Observatory, which gives the dawn at which a SetUp
// expires.
type statusParts[T any] struct {
	conditions func(*T) *[]observatory.Condition
	until      func(*T) **time.Time
	expires    func(*T) **time.Time
	place      *observatory.Location
}

// writeTyped writes the status of one object of a kind in the
// observatory package. It lowers *expiry to the expiry of the object's
// stored SetUp when that is earlier, and returns true when the write
// failed.
func writeTyped[S, T any](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *observatory.Object[S, T], next T, parts statusParts[T], expiry *time.Time) bool {
	stored := *object
	var until *time.Time
	if parts.until != nil {
		until = *parts.until(&stored.Status)
	}
	*expiry = nextExpiry(*expiry, *parts.conditions(&stored.Status), until, parts.place)
	return writeStatus(recorder, client, seen, kind, &stored, reference(kind, stored.Metadata),
		func(copy *observatory.Object[S, T]) any { return copy.Status },
		func(copy *observatory.Object[S, T]) (any, []observatory.Condition) {
			composed := next
			var transitions []observatory.Condition
			*parts.conditions(&composed), transitions = mergeConditions(*parts.conditions(&copy.Status), *parts.conditions(&next))
			var until *time.Time
			if parts.until != nil {
				until = *parts.until(&copy.Status)
				*parts.until(&composed) = until
			}
			if expired := expireSetUp(parts.conditions(&composed), until, parts.place); expired != nil {
				transitions = append(transitions, *expired)
			}
			if parts.expires != nil {
				*parts.expires(&composed) = expiryTime(*parts.conditions(&composed), until, parts.place)
			}
			copy.APIVersion, copy.Kind, copy.Status = observatory.APIVersion, kind.Name, composed
			return composed, transitions
		})
}

// writeStatus writes an object's status when it differs from the
// stored one, and returns true when the write failed. It logs a failed
// write, and the next pass composes the status again. An object deleted
// since the read needs no status, so ErrNotFound is not a failure.
//
// stored returns the object's current status. apply sets the next
// status on the object and returns it with each condition that changed.
// The Event of each changed condition is posted only after the write
// succeeds, so a failed write posts nothing and the next pass finds the
// same change.
func writeStatus[T any, P memo.Object[T]](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *T, about events.ObjectReference, stored func(*T) any, apply func(*T) (any, []observatory.Condition)) bool {
	meta := P(object).GetObjectMeta()
	key, path := kind.Name+"/"+meta.GetName(), objectPath(kind, meta.GetNamespace(), meta.GetName())
	var body []byte
	var transitions []observatory.Condition
	wrote, err := informer.SettleStatus[T, P](client, nil, path, object, func(current *T) bool {
		was, version := stored(current), P(current).GetObjectMeta().GetResourceVersion()
		var next any
		next, transitions = apply(current)
		// If encoding fails, body stays nil, which matches no record
		// (statusMemo.same), so the status is written.
		body, _ = json.Marshal(next)
		if seen.knows(key, version) {
			return !seen.same(key, version, body)
		}
		// The watch's cached copy of a device omits the typed fields,
		// status.limits, and status.properties (watch.go), so for a
		// connected device this comparison always finds a difference and
		// the write is sent. The API server stores nothing for a write
		// that changes nothing, and the memo then records the version,
		// so later passes compare against the memo.
		if equalJSON(was, next) {
			seen.note(key, version, body)
			return false
		}
		return true
	})
	if wrote {
		seen.note(key, P(object).GetObjectMeta().GetResourceVersion(), body)
		for _, c := range transitions {
			recorder.Transition(about, c, badStatus(c))
		}
	}
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		seen.failed = true
		fmt.Fprintf(os.Stderr, "observatory-operator: writing the status of the %s %s: %v\n", kind.Name, meta.GetName(), err)
		return true
	}
	return false
}

// deviceStatus composes one device's status from the pod of its driver
// and from what the driver reports on its INDI server. A device that
// another device's driver serves reads that device's pod and INDI
// device (served.go). The returned reading holds what the device's
// series need (series.go).
func (o *operator) deviceStatus(t *tree, d *device) (deviceStatus, deviceReading) {
	var next deviceStatus
	next.ObservedGeneration = d.object.Metadata.Generation
	if d.runsDriver() {
		next.Driver = d.object.Spec.Driver.Name
		if image, err := drivers.Resolve(d.object.Spec.Driver); err == nil {
			next.Image = image
		}
	}
	fault := o.faultOf(d)
	name, p, hasPod := t.podOf(d)
	if hasPod && d.runsDriver() {
		next.Pod, next.Node = name, p.Spec.NodeName
	}
	var connection indi.Property
	defined := false
	var device *reader
	ref, placed := t.server(d)
	// When two devices on one server declare the same driver, INDI gives
	// both one device name, and the device that runs the driver owns
	// that INDI device. The other device reads nothing and is in Error,
	// and so is a served device that served.go refuses.
	refused := false
	if holder, taken := t.driverHolder(d); taken {
		fault, refused = sharedDriver(holder, ref), true
	}
	if v := t.ownershipOf(d); v.refusal != "" {
		fault, refused = v.refusal, true
	}
	// A device that a person deleted, or moved to another server, keeps
	// its pod on the old server while its deactivation runs (leaves.go).
	// Its status reads that server until the pod is being deleted, so a
	// person can watch the deactivation, such as a dust cap closing.
	at, readable := ref.String(), placed
	left := hasPod && (!placed || p.Metadata.Labels[labelServer] != ref.String())
	if left {
		at, readable = p.Metadata.Labels[labelServer], p.Metadata.DeletionTimestamp == nil
	}
	// The endpoint is the server in the label of the device's pod, and
	// only while the pod is not being deleted. The operator stops the
	// driver before it deletes the pod (serverdrivers.go), so a client
	// that dialed the server of a pod being deleted would find no driver.
	if hasPod && p.Metadata.DeletionTimestamp == nil {
		on := p.Metadata.Labels[labelServer]
		next.Endpoint = &observatory.Endpoint{Service: on, Host: serviceHost(on, o.namespace), Port: serverPort}
	}
	serving := false
	// A device with no pod, or one that the operator refuses, has no
	// connected driver, and the operator can tell that without a read
	// from its server.
	observed := !hasPod || refused
	if server, open := o.servers.get(at); readable && !refused && hasPod && open && server.client.Connected() {
		observed = true
		if indiDevice := t.indiName(server.client, d); indiDevice != "" {
			all := server.client.Properties(indiDevice)
			held, rest := t.split(d, all)
			r := newReader(held)
			next.IndiDevice = indiDevice
			if d.runsDriver() {
				next.Properties = properties(rest)
			}
			next.Standard = standardStatus(d.kind, r)
			next.Limits = limits(r)
			next.Readings = readings(d.kind, r)
			connection, defined = newReader(all).property("CONNECTION")
			device = &r
			serving = serves(server.client, indiDevice, d.kind)
		}
	}
	next.Display = deviceDisplay(d, next.Standard, next.Limits)
	use := o.serverUseOf(t, d)
	// A device whose pod is on a server it has left is releasing there,
	// whatever serverUseOf returns, so the Disconnect step's disconnect
	// reads Disconnecting, not Starting. Once its driver is gone from
	// that server, the pod counts as stopping until the Ready runner
	// (deletes.go) or the deactivation deletes it.
	driverGone := left && !defined
	phaseUse := use
	if left {
		phaseUse = releasing
	}
	next.Phase = devicePhase(hasPod && (driverGone || p.Metadata.DeletionTimestamp != nil), hasPod, defined, phaseUse, connection, fault)
	if refused {
		next.Phase = observatory.DeviceError
	}
	// A connected device that does not define the properties of its
	// kind's interface is Connecting until a step or a runner reports
	// that it lacks them (detect), and Error after that. If the device
	// defines the properties later, it becomes Connected at once.
	lacking := o.lackingOf(d)
	detecting := next.Phase == observatory.DeviceConnected && !serving
	if detecting && lacking != "" {
		next.Phase, fault, detecting = observatory.DeviceError, lacking, false
	} else if detecting {
		next.Phase = observatory.DeviceConnecting
	}
	parent := t.parentFound(d)
	message := deviceMessage(next, d.kind, at, name, fault, use)
	switch {
	case detecting:
		message = fmt.Sprintf("Waiting for %s to define %s", next.IndiDevice, strings.Join(observatory.Standards[d.kind].Detect, " or "))
	case next.Phase == observatory.DeviceIdle && t.waits(d):
		declared, _ := t.declared(d)
		message = "Starts at the next reservation, after " + endOf(t.sessions().holders[declared])
	}
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, string(next.Phase), message)
	if next.Phase == observatory.DeviceConnected {
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, string(next.Phase), deviceMessage(next, d.kind, at, name, "", use))
	}
	next.Conditions = []observatory.Condition{parent, ready}
	next.Procedures = o.runs.list(recordKey(d.kind, d.object.Metadata))
	if next.Phase == observatory.DeviceConnected && device != nil {
		next.Conditions = append(next.Conditions, stateConditions(d, *device)...)
	}
	next.Conditions = withAttention(next.Conditions, next.Procedures, d.object.Status.Conditions, o.activeSince(t.ofDevice(d)))
	for i := range next.Conditions {
		next.Conditions[i].ObservedGeneration = next.ObservedGeneration
	}
	connect, _ := connection.Member("CONNECT")
	_, claimed := t.heldOn(d)
	return next, deviceReading{
		observed:  observed,
		connected: defined && connect.Switch,
		claimed:   claimed,
		leaving:   use == releasing || left || deleting(d.object.Metadata),
		standard:  next.Standard,
	}
}

// devicePhase returns a device's phase. A device with no pod takes its
// phase from the use of its server: Inventory when it names no parent, Idle when
// no reservation needs it or a release has stopped it, and Starting
// while an activation or a Ready reservation's runner creates its pod.
// A device with a pod takes its phase from its CONNECTION property and
// its fault, and a device that a release stops is Disconnecting until its pod is gone.
func devicePhase(stopping, hasPod, defined bool, use serverUse, connection indi.Property, fault string) observatory.DevicePhase {
	connect, _ := connection.Member("CONNECT")
	switch {
	case !hasPod && use == notInstalled:
		return observatory.DeviceInventory
	case !hasPod && (use == idle || use == releasing):
		return observatory.DeviceIdle
	case !hasPod:
		return observatory.DeviceStarting
	case stopping:
		return observatory.DeviceDisconnecting
	case defined && connection.State == indi.Busy && connect.Switch:
		return observatory.DeviceConnecting
	case defined && connection.State == indi.Busy:
		return observatory.DeviceDisconnecting
	case fault != "", defined && connection.State == indi.Alert:
		return observatory.DeviceError
	case defined && connect.Switch:
		return observatory.DeviceConnected
	case use == releasing:
		return observatory.DeviceDisconnecting
	}
	return observatory.DeviceStarting
}

// deviceMessage returns the Ready message of a device: what it is
// doing or waiting for. server is the server the device runs on, or
// the one it left while its driver still runs there, and pod is the
// name of the device's pod.
func deviceMessage(s deviceStatus, kind observatory.Kind, server, pod, fault string, use serverUse) string {
	switch {
	case fault != "":
		return "Failed: " + fault
	case s.Phase == observatory.DeviceInventory:
		return notInstalledMessage(kind)
	case s.Phase == observatory.DeviceIdle:
		return "Not reserved"
	case s.Phase == observatory.DeviceStarting && s.Pod == "" && use == maintained:
		return "Creating pod " + pod
	case s.Phase == observatory.DeviceStarting && s.Pod == "":
		return "Waiting for activation to create pod " + pod
	case s.Phase == observatory.DeviceDisconnecting && s.IndiDevice == "":
		return "Stopping pod " + pod
	case s.IndiDevice == "":
		return "Waiting for its driver on " + server
	}
	return fmt.Sprintf("%s on %s", s.Phase, server)
}
