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
		select {
		case <-ctx.Done():
			return
		case <-wake:
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
		writeStatus(client, seen, d.kind, &object,
			func(held *deviceObject) any { return held.Status },
			func(held *deviceObject) any {
				next.Conditions = mergeConditions(held.Status.Conditions, next.Conditions)
				held.APIVersion, held.Kind, held.Status = observatory.APIVersion, d.kind.Name, next
				return next
			})
	}
	for _, telescope := range t.telescopes {
		next := o.telescopeStatus(t, telescope, composed)
		writeTyped(client, seen, observatory.TelescopeKind, telescope, next, func(s *observatory.TelescopeStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, site := range t.observatories {
		next := o.observatoryStatus(t, site, composed)
		writeTyped(client, seen, observatory.ObservatoryKind, site, next, func(s *observatory.ObservatoryStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, train := range t.trains {
		next := trainStatus(t, train, composed)
		writeTyped(client, seen, observatory.OpticalTrainKind, train, next, func(s *observatory.OpticalTrainStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, tube := range t.tubes {
		next := tubeStatus(t, tube)
		writeTyped(client, seen, observatory.OpticalTubeKind, tube, next, func(s *observatory.OpticalTubeStatus) *[]observatory.Condition { return &s.Conditions })
	}
	for _, guider := range t.guiders {
		next := guiderStatus(t, guider)
		writeTyped(client, seen, observatory.GuiderKind, guider, next, func(s *observatory.GuiderStatus) *[]observatory.Condition { return &s.Conditions })
	}
}

// writeTyped writes one status of a kind of the observatory package.
// conditions answers the status's conditions, which keep their
// transition times when their status holds.
func writeTyped[S, T any](client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *observatory.Object[S, T], next T, conditions func(*T) *[]observatory.Condition) {
	held := *object
	writeStatus(client, seen, kind, &held,
		func(copy *observatory.Object[S, T]) any { return copy.Status },
		func(copy *observatory.Object[S, T]) any {
			*conditions(&next) = mergeConditions(*conditions(&copy.Status), *conditions(&next))
			copy.APIVersion, copy.Kind, copy.Status = observatory.APIVersion, kind.Name, next
			return next
		})
}

// writeStatus writes one status when it differs from the stored one,
// and logs a failure: the next window composes the status again and
// writes it then. An object deleted since the read needs no status.
// stored answers the object's status, and apply sets the next status
// on the object and answers it.
func writeStatus[T any, P memo.Object[T]](client *apiclient.Client, seen *statusMemo, kind observatory.Kind, object *T, stored, apply func(*T) any) {
	meta := P(object).GetObjectMeta()
	key, path := kind.Name+"/"+meta.GetName(), objectPath(kind, meta.GetNamespace(), meta.GetName())
	var body []byte
	wrote, err := informer.SettleStatus[T, P](client, nil, path, object, func(held *T) bool {
		was, version := stored(held), P(held).GetObjectMeta().GetResourceVersion()
		next := apply(held)
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
	}
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
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
	ref, placed := t.server(d)
	if server, open := o.servers.get(ref.String()); placed && hasPod && open && server.client.Connected() {
		indiDevice, err := indiName(server.client, t.devicesOn(ref), d)
		if err != nil {
			fault = err.Error()
		}
		if indiDevice != "" {
			r := reader{c: server.client, name: indiDevice}
			next.IndiDevice = indiDevice
			next.Properties = properties(r)
			next.Readings = readings(d.kind, r)
			connection, defined = r.property("CONNECTION")
		}
	}
	next.Phase = devicePhase(hasPod && p.Metadata.DeletionTimestamp != nil, hasPod, defined, connection, fault)
	parent := condition(observatory.ConditionParentFound, observatory.ConditionTrue, "Found", "every resource up to the Observatory exists")
	if missing := t.missingParent(d); missing != "" {
		parent = condition(observatory.ConditionParentFound, observatory.ConditionFalse, "ParentMissing", missing+" does not exist")
	}
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, string(next.Phase), deviceMessage(next, ref, fault))
	if next.Phase == observatory.DeviceConnected {
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, string(next.Phase), deviceMessage(next, ref, ""))
	}
	next.Conditions = []observatory.Condition{parent, ready}
	for i := range next.Conditions {
		next.Conditions[i].ObservedGeneration = next.ObservedGeneration
	}
	return next
}

// devicePhase answers a device's phase from what the operator observes.
func devicePhase(stopping, hasPod, defined bool, connection indi.Property, fault string) observatory.DevicePhase {
	connect, _ := connection.Member("CONNECT")
	switch {
	case !hasPod:
		return observatory.DeviceInventory
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

func deviceMessage(s deviceStatus, ref serverRef, fault string) string {
	switch {
	case fault != "":
		return fault
	case s.Phase == observatory.DeviceInventory:
		return "no reservation needs the device, and it has no pod"
	case s.IndiDevice == "":
		return fmt.Sprintf("the pod %s runs, and its driver has not defined its device on %s", s.Pod, ref)
	}
	return fmt.Sprintf("%s is %s on %s", s.IndiDevice, s.Phase, ref)
}
