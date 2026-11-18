package main

// A person who fixes the spec that failed an activation step requests
// a retry with the annotation observatory.liken.sh/retry. The session's
// record in the Reservation's status still has the failed spec
// (records.go), so before it runs any step, the runner records the
// session again from the current specs. A pod built from the old record
// no longer matches the new one, and ensure replaces it.
//
// The retry also undoes what the old record started and the new record
// leaves out. A device that is missing from the new record leaves the
// session at once, as a deleted device leaves a Ready session (deletes.go): its
// deactivation runs, its driver stops, its pod is deleted, and its power
// output is switched off unless another device draws from it. A device
// whose recorded output changed has its old output switched off the
// same way. Then the steps that read a changed part of the record run
// again (rerunFrom). PowerOn switches on a changed output, and StartSite
// or PowerOn starts the drivers of a device that the new record adds.

import (
	"context"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recordAgain records the reservation's session again from the current
// specs, and waits until the operator's store has the new record. The
// observatory's part of the record keeps the devices that the other
// reservations on the same observatory server recorded, as StartSite
// would, so their sessions keep their devices. A retry of a
// deactivation step records nothing, because deactivation stops only
// what the session started.
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
		next.Outbox = r.res.Spec.Outbox
		next.FITSKeywords = t.fitsKeywordsOf(ref, next.Observatory, t.devicesOn(ref), r.res.Spec.FITSKeywords)
	}
	r.status.Session = next
	r.updateEditsPending(r.o.snapshot())
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

// undoOld releases each device that the old record ran on a server and
// the new record leaves out (releaseLeft). Then it switches off each
// output that the old record powered a device from and the new record
// does not, unless another device still draws from it. before is the
// tree under the old record, whose device specs name the outputs that
// the session switched on.
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

// rerunFrom sets each finished activation step back to Pending, from
// the first step that reads a part of the record that the retry
// changed:
//
//   - StartSite for the observatory's devices.
//   - PowerOn for the telescope's devices, because it starts their
//     drivers on the server and switches on their outputs.
//   - PowerOn for the outbox, because it checks the claim and the
//     camera pods that mount it.
//   - Configure for the trains, the tubes, and the FITS keywords.
//   - StartGuider for the guiders.
//
// The later steps read what that step starts, so they run again too. A
// step that runs again finds each unchanged device as the earlier run
// left it. An activation that is already done stays done (activation).
func (r *runner) rerunFrom(old, next *observatory.Session) {
	var first observatory.StepName
	switch {
	case !equalJSON(old.Site, next.Site):
		first = observatory.StepStartSite
	case !equalJSON(old.Devices, next.Devices) || !equalJSON(old.Outbox, next.Outbox):
		first = observatory.StepPowerOn
	case !equalJSON(old.Trains, next.Trains) || !equalJSON(old.Tubes, next.Tubes) || !equalJSON(old.FITSKeywords, next.FITSKeywords):
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

// forgotten returns the tree as if this reservation had recorded no
// session. With failedSites, it also drops the session of each
// reservation whose StartSite step failed. recordAgain asks for that
// for the observatory's part of the record, because such a reservation
// may have recorded a spec that a person has since fixed, and its own
// retry records the site again. The telescope's part keeps those
// sessions, because a device runs on one server at a time
// (tree.server), and the devices they name may run on the
// observatory's server for that session.
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
