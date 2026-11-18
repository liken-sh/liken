package main

// When a device leaves a server while its Telescope or Observatory is
// Active, because a person deleted it or moved it to another server,
// setDrivers in serverdrivers.go stops its driver. The operator first
// runs the device's deactivation through that driver, so that a dust
// cap, for example, is not left open.
//
// The device's finalizer keeps its object until its pod is gone, so the
// spec is still readable. The run uses the time the parent turned
// Active as its since, the same as the activation. References in the
// actions resolve against the server the device left, and an action on
// a device that is not connected is skipped. When the run fails, the
// operator posts ProcedureFailed and stops the driver anyway.
//
// A deleted device that has an activation runs its deactivation only
// if that activation ran in the current Active period, because a dust
// cap deleted before the Activation step reached it was never opened.
// A device with no activation always runs its deactivation, because a
// person may have changed it by hand during the session, such as
// turning on a flat panel.
//
// When a person deletes a device together with its reservation, as
// `kubectl delete -f` on a whole observatory does, the reservation's
// Deactivation step runs the device's deactivation instead, through the
// server its pod runs on (tree.runningOn).

import (
	"context"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// leavers returns the devices of pods that left the server ref. It
// leaves out a pod whose device object is gone or was created again
// with a new UID, and a device that is still placed on ref.
func leavers(t *tree, ref serverRef, pods []string) []resource {
	var out []resource
	for _, name := range pods {
		p, ok := t.pods[name]
		if !ok || len(p.Metadata.OwnerReferences) == 0 {
			continue
		}
		l := p.Metadata.Labels
		d, ok := t.deviceByKey(l[labelKind] + "/" + l[labelResource])
		if !ok || d.object.Metadata.UID != p.Metadata.OwnerReferences[0].UID {
			continue
		}
		if now, placed := t.server(d); placed && now == ref {
			continue
		}
		out = append(out, leaver(t, d, ref))
	}
	return out
}

// leaver returns the device as a resource that belongs to the server it
// left, so that references in its actions resolve against that parent.
func leaver(t *tree, d *device, ref serverRef) resource {
	r := t.ofDevice(d)
	r.telescope, r.observatory = "", ref.name
	if ref.kind == observatory.TelescopeKind {
		r.telescope, r.observatory = ref.name, ""
		if telescope, ok := t.telescopes[ref.name]; ok {
			r.observatory = telescope.Spec.Observatory
		}
	}
	return r
}

func (o *operator) deactivateLeavers(ctx context.Context, t *tree, ref serverRef, pods []string) {
	o.deactivateLeft(ctx, ref, leavers(t, ref, pods))
}

// deactivateLeft runs the deactivations of the devices in left in
// parallel and returns when every run has ended. It does nothing
// unless ref is Active.
func (o *operator) deactivateLeft(ctx context.Context, ref serverRef, left []resource) {
	key := telescopeKey(ref.name)
	if ref.kind == observatory.ObservatoryKind {
		key = observatoryKey(ref.name)
	}
	state := o.activity.get(key)
	if !state.active {
		return
	}
	var group sync.WaitGroup
	for _, r := range left {
		if len(r.procedures.Deactivation) == 0 || (deleting(r.meta) && len(r.procedures.Activation) > 0 && !o.activated(r, state.since)) {
			continue
		}
		group.Go(func() {
			err := o.runProcedure(ctx, procCall{res: r, trigger: observatory.TriggerDeactivation,
				actions: r.procedures.Deactivation, since: state.since, ending: true, on: ref})
			if err != nil {
				o.logf("%s left %s: %v", r, ref, err)
			}
		})
	}
	group.Wait()
}

// activated reports whether the device's activation ran in the Active
// period that began at since.
func (o *operator) activated(r resource, since time.Time) bool {
	run, ok := o.runs.get(r.record(), observatory.TriggerActivation)
	return ok && isRunFor(run, since, time.Time{})
}
