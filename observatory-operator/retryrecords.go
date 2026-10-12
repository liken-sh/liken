package main

// A person who fixes the spec that failed an activation step asks for
// a retry with the annotation observatory.liken.sh/retry. The record of
// the session holds the spec that failed (records.go), so a step that
// ran again with it would fail the same way. A retry is the person
// saying that the specs are fixed, so the runner records the session
// again from the specs as they are, before any step runs again. A pod
// that the record built with the old spec no longer matches the record,
// and the step that runs again replaces it (ensure).
//
// The session acts on what it recorded, so the retry also undoes what
// the old record started and the new one does not. A device that the
// new record leaves out leaves the session at once, as a deleted device
// leaves a Ready session (deletes.go): its deactivation runs, its driver
// stops, its pod goes, and the output that the old record switched on
// goes off when no device draws from it. A device whose recorded output
// changed has its old output switched off the same way. Each step that
// reads a part of the record that changed runs again, from the first
// one (rerunFrom), so PowerOn switches on a changed output, and
// StartSite or PowerOn gives the server's drivers to a device that the
// new record adds.

import (
	"context"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recordAgain records the parts of the session that the reservation
// recorded, from the specs as they are now, and waits until the store
// holds the new record. The part of the observatory's server holds the
// set that the other reservations that hold the server recorded, as
// the StartSite step would, so a session that runs there keeps its
// devices. A retry of a deactivation step records nothing: the release
// stops what the session started.
func (r *runner) recordAgain(ctx context.Context) (notes []string, err error) {
	old := r.status.Session
	if old == nil || isDeactivation(r.status.Step) {
		return nil, nil
	}
	if old.Observatory != "" {
		lock := r.o.siteLock(old.Observatory)
		if err := lock.acquire(ctx); err != nil {
			return nil, err
		}
		defer lock.release()
	}
	before := r.o.snapshot()
	next := &observatory.Session{Observatory: old.Observatory}
	if old.Observatory != "" {
		t := r.forgotten(true)
		site := serverRef{observatory.ObservatoryKind, old.Observatory}
		if devices := t.devicesOn(site); len(devices) > 0 {
			next.Site = t.recordOf(site, devices)
		}
	}
	if len(old.Devices) > 0 {
		t := r.forgotten(false)
		ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
		next.Devices = t.recordOf(ref, t.devicesOn(ref))
		next.Trains, next.Tubes, next.Guiders = t.opticsOf(ref.name)
	}
	r.status.Session = next
	r.rerunFrom(old, next)
	if err := r.o.send(ctx, nil, "recording the session again in the status of the Reservation "+r.name, func() error { return r.save(ctx) }); err != nil {
		return nil, err
	}
	if err := r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || equalJSON(res.Status.Session, next), "", nil
	}); err != nil {
		return nil, err
	}
	return r.undoOld(ctx, before, old, next)
}

// undoOld releases each device that a server of the old record runs
// and the new record leaves out (releaseLeft), and then switches off
// each output that the old record powered a device from and the new
// record does not, unless another device draws from it (unpowered).
// before is the tree of the old record, whose spec of each device names
// the output that the session switched on. It answers what it switched
// off.
func (r *runner) undoOld(ctx context.Context, before *tree, old, next *observatory.Session) ([]string, error) {
	after := r.o.snapshot()
	var servers []serverRef
	if site, ok := after.observatories[old.Observatory]; ok {
		ref := serverRef{observatory.ObservatoryKind, old.Observatory}
		servers = append(servers, ref)
		if err := r.releaseLeft(ctx, after, before, ref, reference(ref.kind, site.Metadata)); err != nil {
			return nil, err
		}
	}
	if telescope, ok := after.telescopes[r.res.Spec.Telescope]; ok && len(old.Devices) > 0 {
		ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
		servers = append(servers, ref)
		if err := r.releaseLeft(ctx, after, before, ref, reference(ref.kind, telescope.Metadata)); err != nil {
			return nil, err
		}
	}
	var unpowered []*device
	for _, part := range []struct {
		old, next map[string]observatory.SessionDevice
	}{{old.Site, next.Site}, {old.Devices, next.Devices}} {
		for key, was := range part.old {
			d, ok := before.deviceByKey(key)
			if now, kept := part.next[key]; !ok || was.Power == nil || (kept && equalJSON(was.Power, now.Power)) {
				continue
			}
			copied := *d
			copied.object.Spec.Power = was.Power
			unpowered = append(unpowered, &copied)
		}
	}
	now := r.o.snapshot()
	return r.o.setOutputs(ctx, now, now.unpowered(unpowered, serverRef{}), false)
}

// rerunFrom sets Pending each activation step that is done, from the
// first one that reads a part of the record that the retry changed:
// StartSite for the observatory's devices, PowerOn for the telescope's
// devices, because it gives the server their drivers and switches on
// their outputs, Configure for the trains and tubes, and StartGuider
// for the Guiders. The steps after it read what it starts, so they run
// again too, and the retry names the first one. A step that runs again finds each device that did not
// change as it left it, and an activation run that is done stays done
// (activation).
func (r *runner) rerunFrom(old, next *observatory.Session) {
	var first observatory.StepName
	switch {
	case !equalJSON(old.Site, next.Site):
		first = observatory.StepStartSite
	case !equalJSON(old.Devices, next.Devices):
		first = observatory.StepPowerOn
	case !equalJSON(old.Trains, next.Trains) || !equalJSON(old.Tubes, next.Tubes):
		first = observatory.StepConfigure
	case !equalJSON(old.Guiders, next.Guiders):
		first = observatory.StepStartGuider
	default:
		return
	}
	from := slices.Index(observatory.ActivationSteps, first)
	if from < slices.Index(observatory.ActivationSteps, r.status.Step) {
		r.status.Step = first
	}
	for i := range r.status.Steps {
		s := &r.status.Steps[i]
		if slices.Index(observatory.ActivationSteps, s.Name) >= from && (s.State == observatory.StepDone || s.State == observatory.StepSkipped) {
			s.State, s.StartTime, s.StopTime, s.Summary, s.Actions = observatory.StepPending, nil, nil, "", nil
		}
	}
}

// forgotten answers the tree as if the reservation had recorded
// nothing. With failedSites, it also leaves out the record of each
// reservation whose StartSite step failed, for the part of the
// observatory's server: such a reservation may have recorded the spec
// that a person fixed, and its own retry records the site again. The
// part of the telescope's server keeps those records, because the
// devices that they name may run on the observatory's server for that
// session, and a device runs on one server at a time (tree.server).
func (r *runner) forgotten(failedSites bool) *tree {
	t := r.o.stores.read(r.o.namespace)
	for other, res := range t.reservations {
		if res.Status.Session != nil && (other == r.name || (failedSites && failedSite(res))) {
			copied := *res
			copied.Status.Session = nil
			t.reservations[other] = &copied
		}
	}
	t.applyRecords()
	return t
}
