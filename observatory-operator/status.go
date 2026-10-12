package main

// The status writer writes the status of every resource except a
// Reservation, whose runner writes its own. INDI updates arrive several
// times a second while a mount slews or a camera cools, so the writer
// composes every status at most once per statusWindow, and writes only
// the statuses that changed. The API server then receives at most one
// write a window for each object.

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

// statusWindow is the clock that coalesces the status writes.
const statusWindow = time.Second

func (o *operator) writeStatuses(ctx context.Context) {
	seen := &statusMemo{}
	first := newFirstRead()
	for {
		wake := o.changed.wait()
		var held, expiry time.Time
		if o.stores.ready() && o.seeded.Load() {
			t := o.snapshot()
			if held = first.hold(ctx, o, t); held.IsZero() {
				expiry = o.writeAll(ctx, t, seen)
			}
		}
		// The timers are clocks: the end of the hold (firstread.go),
		// and the expiry of the next SetUp, which no watch event marks
		// (setup.go).
		end, stopEnd := clock(held)
		expires, stopExpires := clock(expiry)
		// After a failed write, the writer waits for the next window,
		// not for the next change: no event follows a failure, and the
		// status stays stale until the writer composes it again.
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

// clock answers a channel that receives at a time, and the function
// that stops its timer, or a nil channel when the time is zero.
func clock(at time.Time) (<-chan time.Time, func()) {
	if at.IsZero() {
		return nil, func() {}
	}
	timer := time.NewTimer(time.Until(at))
	return timer.C, func() { timer.Stop() }
}

// writeAll composes and writes every status but the reservations', and
// sets the namespace's series from what it composed (series.go). The
// pass counts in the metrics under each kind that it wrote (metrics.go).
// It answers the next expiry of a person's SetUp, or zero when no SetUp
// holds.
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
			func(held *deviceObject) any { return held.Status },
			func(held *deviceObject) (any, []observatory.Condition) {
				composed := next
				var transitions []observatory.Condition
				composed.Conditions, transitions = mergeConditions(held.Status.Conditions, next.Conditions)
				composed.SetUpUntil = held.Status.SetUpUntil
				if expired := expireSetUp(&composed.Conditions, composed.SetUpUntil, place); expired != nil {
					transitions = append(transitions, *expired)
				}
				held.APIVersion, held.Kind, held.Status = observatory.APIVersion, d.kind.Name, composed
				return composed, transitions
			})
		times.add(d.kind, began, failed)
	}
	// A telescope's status names its guider's phase and state, so the
	// guiders' statuses are composed first.
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

// statusParts names the parts of one kind's status that the writer
// merges with the stored status: its conditions, which keep their
// transition times while their status holds, and a person's
// setUpUntil, nil for a kind that has none. place is the location of
// the resource's Observatory, for the dawn that expires a SetUp.
type statusParts[T any] struct {
	conditions func(*T) *[]observatory.Condition
	until      func(*T) **time.Time
	place      *observatory.Location
}

// writeTyped writes one status of a kind of the observatory package,
// and moves expiry to the expiry of its stored SetUp when that comes
// sooner. It reports whether the write failed, as writeStatus does.
func writeTyped[S, T any](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *observatory.Object[S, T], next T, parts statusParts[T], expiry *time.Time) bool {
	held := *object
	var until *time.Time
	if parts.until != nil {
		until = *parts.until(&held.Status)
	}
	*expiry = nextExpiry(*expiry, *parts.conditions(&held.Status), until, parts.place)
	return writeStatus(recorder, client, seen, kind, &held, reference(kind, held.Metadata),
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
			copy.APIVersion, copy.Kind, copy.Status = observatory.APIVersion, kind.Name, composed
			return composed, transitions
		})
}

// writeStatus writes one status when it differs from the stored one,
// and logs a failure: the next window composes the status again and
// writes it then. An object deleted since the read needs no status.
// stored answers the object's status, and apply sets the next status
// on the object and answers it, with each condition that transitioned
// from the stored status. Each transition posts its Event only after
// the write lands, so a refused write posts nothing, and the next
// window finds the same transition again. It reports whether the write
// failed, for the pass's metrics.
func writeStatus[T any, P memo.Object[T]](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *T, about events.ObjectReference, stored func(*T) any, apply func(*T) (any, []observatory.Condition)) bool {
	meta := P(object).GetObjectMeta()
	key, path := kind.Name+"/"+meta.GetName(), objectPath(kind, meta.GetNamespace(), meta.GetName())
	var body []byte
	var transitions []observatory.Condition
	wrote, err := informer.SettleStatus[T, P](client, nil, path, object, func(held *T) bool {
		was, version := stored(held), P(held).GetObjectMeta().GetResourceVersion()
		var next any
		next, transitions = apply(held)
		// An error leaves body nil, which matches no record (statusMemo.same).
		body, _ = json.Marshal(next)
		if seen.knows(key, version) {
			return !seen.same(key, version, body)
		}
		// The cached copy of a device holds no typed fields, no
		// status.limits, and no status.properties (watch.go), so for a
		// connected device this comparison fails, and the write goes
		// out. The API server finds it unchanged and stores nothing,
		// and the memo then records the version.
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

// deviceStatus composes one device's status from the pod of the driver
// that serves it and from what that driver reports on its server. A
// served device reads its owner's pod and INDI device (served.go). The
// reading holds what the series of the device need (series.go).
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
	// A device whose driver another device runs has no device of its
	// own on the server. The device of that name is the other one's. A
	// served device that the tree refuses has none either.
	refused := false
	if holder, taken := t.driverTakenBy(d); taken {
		fault, refused = sharedDriver(holder, ref), true
	}
	if v := t.verdict(d); v.refusal != "" {
		fault, refused = v.refusal, true
	}
	// A device that a person deleted keeps its pod on its server while
	// its deactivation runs (leaves.go). Its status reads that server
	// until the driver stops, so a person who watches the delete reads
	// the cap close.
	at, readable := ref.String(), placed
	left := hasPod && (!placed || p.Metadata.Labels[labelServer] != ref.String())
	if left {
		at, readable = p.Metadata.Labels[labelServer], p.Metadata.DeletionTimestamp == nil
	}
	// The endpoint names the server in the label of the pod of the
	// device's driver, while that pod runs and is not stopping. A
	// device's driver stops on its server before the operator deletes
	// its pod (serverdrivers.go), so a client that dials the endpoint of
	// a device whose pod is stopping, or that has no pod, finds no
	// driver there.
	if hasPod && p.Metadata.DeletionTimestamp == nil {
		on := p.Metadata.Labels[labelServer]
		next.Endpoint = &observatory.Endpoint{Service: on, Host: serviceHost(on, o.namespace), Port: serverPort}
	}
	serving := false
	// A device with no pod, or one that the operator refuses, has no
	// driver of its own, so the operator knows that it is not connected.
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
	standing := o.standingOf(t, d)
	// A pod that a device's server no longer runs is one that a Ready
	// runner deletes (deletes.go), or one that deactivation deletes. It
	// reads as stopping once its driver is gone from that server. Its
	// device is releasing there whatever its parents say, so a device
	// that the Disconnect step disconnects on the server it left reads
	// Disconnecting, and not Starting, until its pod is gone.
	stray := left && !defined
	phaseStanding := standing
	if left {
		phaseStanding = releasing
	}
	next.Phase = devicePhase(hasPod && (stray || p.Metadata.DeletionTimestamp != nil), hasPod, defined, phaseStanding, connection, fault)
	if refused {
		next.Phase = observatory.DeviceError
	}
	// A connected device that does not serve its resource's interface
	// is Error once a step or a runner has decided so (detect), and
	// Connecting until then. A device that defines the properties later
	// is Connected at once.
	lacking := o.lackingOf(d)
	detecting := next.Phase == observatory.DeviceConnected && !serving
	if detecting && lacking != "" {
		next.Phase, fault, detecting = observatory.DeviceError, lacking, false
	} else if detecting {
		next.Phase = observatory.DeviceConnecting
	}
	parent := t.parentFound(d)
	message := deviceMessage(next, d.kind, at, name, fault, standing)
	switch {
	case detecting:
		message = fmt.Sprintf("Waiting for %s to define %s", next.IndiDevice, strings.Join(observatory.Standards[d.kind].Detect, " or "))
	case next.Phase == observatory.DeviceIdle && t.waits(d):
		declared, _ := t.declared(d)
		message = "Starts at the next reservation, after " + endOf(t.sessions().holders[declared])
	}
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, string(next.Phase), message)
	if next.Phase == observatory.DeviceConnected {
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, string(next.Phase), deviceMessage(next, d.kind, at, name, "", standing))
	}
	next.Conditions = []observatory.Condition{parent, ready}
	next.Procedures = o.runs.list(recordKey(d.kind, d.object.Metadata))
	if next.Phase == observatory.DeviceConnected && device != nil {
		next.Conditions = append(next.Conditions, stateConditions(d, *device)...)
	}
	next.Conditions = withAttention(next.Conditions, next.Procedures, d.object.Status.Conditions)
	for i := range next.Conditions {
		next.Conditions[i].ObservedGeneration = next.ObservedGeneration
	}
	connect, _ := connection.Member("CONNECT")
	_, claimed := t.heldOn(d)
	return next, deviceReading{
		observed:  observed,
		connected: defined && connect.Switch,
		claimed:   claimed,
		leaving:   standing == releasing || left || deleting(d.object.Metadata),
		standard:  next.Standard,
	}
}

// devicePhase answers a device's phase from what the operator observes.
// A device with no pod takes its phase from its standing: Inventory on
// the shelf, Idle when no reservation needs it or a release stopped it,
// and Starting from the start of activation, and while a Ready
// reservation's runner creates a pod that is gone. A device that a
// release stops is Disconnecting from its disconnect until its pod is
// gone, while the operator stops its driver or its server.
func devicePhase(stopping, hasPod, defined bool, standing standing, connection indi.Property, fault string) observatory.DevicePhase {
	connect, _ := connection.Member("CONNECT")
	switch {
	case !hasPod && standing == onShelf:
		return observatory.DeviceInventory
	case !hasPod && (standing == idle || standing == releasing):
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
	case standing == releasing:
		return observatory.DeviceDisconnecting
	}
	return observatory.DeviceStarting
}

// deviceMessage says what a device does, or waits for. server names
// the server that the device runs on, or the one it left while its
// driver still runs there, and pod names the device's pod.
func deviceMessage(s deviceStatus, kind observatory.Kind, server, pod, fault string, standing standing) string {
	switch {
	case fault != "":
		return "Failed: " + fault
	case s.Phase == observatory.DeviceInventory:
		return shelfMessage(kind)
	case s.Phase == observatory.DeviceIdle:
		return "Not reserved"
	case s.Phase == observatory.DeviceStarting && s.Pod == "" && standing == kept:
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
