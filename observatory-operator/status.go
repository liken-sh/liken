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
	for {
		wake := o.changed.wait()
		if o.stores.ready() {
			o.writeAll(ctx, o.snapshot(), seen)
		}
		// After a failed write, the writer waits for the next window,
		// not for the next change: no event follows a failure, and the
		// status stays stale until the writer composes it again.
		if !seen.failed {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
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

// writeAll composes and writes every status but the reservations'.
func (o *operator) writeAll(ctx context.Context, t *tree, seen *statusMemo) {
	client := o.client.WithContext(ctx)
	seen.begin()
	composed := map[string]deviceStatus{}
	for _, d := range t.devices {
		next := o.deviceStatus(t, d)
		composed[d.key()] = next
		object := d.object
		writeStatus(o.recorder, client, seen, d.kind, &object, reference(d.kind, object.Metadata),
			func(held *deviceObject) any { return held.Status },
			func(held *deviceObject) (any, []observatory.Condition) {
				composed := next
				var transitions []observatory.Condition
				composed.Conditions, transitions = mergeConditions(held.Status.Conditions, next.Conditions)
				held.APIVersion, held.Kind, held.Status = observatory.APIVersion, d.kind.Name, composed
				return composed, transitions
			})
	}
	// A telescope's status names its guider's phase and state, so the
	// guiders' statuses are composed first.
	guiders := map[string]observatory.GuiderStatus{}
	for name, guider := range t.guiders {
		guiders[name] = o.guiderStatus(t, guider)
	}
	for _, telescope := range t.telescopes {
		next := o.telescopeStatus(t, telescope, composed, guiders)
		writeTyped(o.recorder, client, seen, observatory.TelescopeKind, telescope, next, func(s *observatory.TelescopeStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, site := range t.observatories {
		next := o.observatoryStatus(t, site, composed)
		writeTyped(o.recorder, client, seen, observatory.ObservatoryKind, site, next, func(s *observatory.ObservatoryStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, train := range t.trains {
		next := trainStatus(t, train, composed)
		writeTyped(o.recorder, client, seen, observatory.OpticalTrainKind, train, next, func(s *observatory.OpticalTrainStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, tube := range t.tubes {
		next := tubeStatus(t, tube)
		writeTyped(o.recorder, client, seen, observatory.OpticalTubeKind, tube, next, func(s *observatory.OpticalTubeStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for name, guider := range t.guiders {
		next := guiders[name]
		writeTyped(o.recorder, client, seen, observatory.GuiderKind, guider, next, func(s *observatory.GuiderStatus) *[]observatory.Condition { return &s.Conditions })
	}
}

// writeTyped writes one status of a kind of the observatory package.
// conditions answers the status's conditions, which keep their
// transition times when their status holds.
func writeTyped[S, T any](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *observatory.Object[S, T], next T, conditions func(*T) *[]observatory.Condition) {
	held := *object
	writeStatus(recorder, client, seen, kind, &held, reference(kind, held.Metadata),
		func(copy *observatory.Object[S, T]) any { return copy.Status },
		func(copy *observatory.Object[S, T]) (any, []observatory.Condition) {
			composed := next
			var transitions []observatory.Condition
			*conditions(&composed), transitions = mergeConditions(*conditions(&copy.Status), *conditions(&next))
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
// window finds the same transition again.
func writeStatus[T any, P memo.Object[T]](recorder *events.Recorder, client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *T, about events.ObjectReference, stored func(*T) any, apply func(*T) (any, []observatory.Condition)) {
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
	}
}

// deviceStatus composes one device's status from its pod and from what
// its driver reports on its server.
func (o *operator) deviceStatus(t *tree, d *device) deviceStatus {
	var next deviceStatus
	next.ObservedGeneration = d.object.Metadata.Generation
	next.Driver = d.object.Spec.Driver.Name
	if image, err := drivers.Resolve(d.object.Spec.Driver); err == nil {
		next.Image = image
	}
	fault := o.faultOf(d)
	name, _ := objectName(d.kind, d.name())
	p, hasPod := t.pods[name]
	if hasPod {
		next.Pod, next.Node = name, p.Spec.NodeName
	}
	var connection indi.Property
	defined := false
	var device *reader
	maximum := func(string, string) (float64, bool) { return 0, false }
	ref, placed := t.server(d)
	// A device whose driver another device runs has no device of its
	// own on the server. The device of that name is the other one's.
	holder, taken := t.driverTakenBy(d)
	if taken {
		fault = sharedDriver(holder, ref)
	}
	if server, open := o.servers.get(ref.String()); placed && !taken && hasPod && open && server.client.Connected() {
		if indiDevice := indiName(server.client, d); indiDevice != "" {
			r := reader{c: server.client, name: indiDevice}
			next.IndiDevice = indiDevice
			next.Properties = properties(r)
			next.Readings = readings(d.kind, r)
			connection, defined = r.property("CONNECTION")
			maximum = r.maximum
			device = &r
		}
	}
	next.Display = deviceDisplay(d, next.Readings, maximum)
	standing := o.standingOf(t, d)
	// A pod that a device's server no longer runs is one that a Ready
	// runner deletes (moves.go), or one that deactivation deletes.
	stray := hasPod && (!placed || p.Metadata.Labels[labelServer] != ref.String())
	next.Phase = devicePhase(hasPod && (stray || p.Metadata.DeletionTimestamp != nil), hasPod, defined, standing, connection, fault)
	if taken {
		next.Phase = observatory.DeviceError
	}
	parent := parentCondition(t.missingParent(d))
	if standing == onShelf {
		parent = shelved()
	}
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, string(next.Phase), deviceMessage(next, d.kind, ref, name, fault, standing))
	if next.Phase == observatory.DeviceConnected {
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, string(next.Phase), deviceMessage(next, d.kind, ref, name, "", standing))
	}
	next.Conditions = []observatory.Condition{parent, ready}
	if next.Phase == observatory.DeviceConnected && device != nil {
		next.Conditions = append(next.Conditions, stateConditions(d, *device)...)
	}
	for i := range next.Conditions {
		next.Conditions[i].ObservedGeneration = next.ObservedGeneration
	}
	return next
}

// devicePhase answers a device's phase from what the operator observes.
// A device with no pod takes its phase from its standing: Inventory on
// the shelf, Idle when no reservation needs it, and Starting from the
// start of activation, and while a Ready reservation's runner creates a
// pod that is gone.
func devicePhase(stopping, hasPod, defined bool, standing standing, connection indi.Property, fault string) observatory.DevicePhase {
	connect, _ := connection.Member("CONNECT")
	switch {
	case !hasPod && standing == onShelf:
		return observatory.DeviceInventory
	case !hasPod && standing == idle:
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
	}
	return observatory.DeviceStarting
}

// deviceMessage says what a device does, or waits for. pod names the
// device's pod.
func deviceMessage(s deviceStatus, kind observatory.Kind, ref serverRef, pod, fault string, standing standing) string {
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
		return "Waiting for its driver on " + ref.String()
	}
	return fmt.Sprintf("%s on %s", s.Phase, ref)
}
