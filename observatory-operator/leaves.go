package main

// A device can leave a running server while its Telescope or
// Observatory is Active: a person puts it on the shelf, or names
// another parent. Its driver then stops (serverdrivers.go), and a dust
// cap left open would stay open. So before the driver stops, the
// operator runs the device's deactivation through that driver. The
// run ends the device's part in the activity that its activation began,
// so its since is the time its old parent turned Active, the same as
// the since of its activation. An operator restart during the run
// finds the device still in the server's annotation, and resumes the
// run from its record.
//
// The run acts on the device where it was: a reference in an action
// resolves against the old parent, and the action reaches the device
// through the old server. A device that is not connected skips each
// action, as in the Deactivation step. A failure posts
// ProcedureFailed, and the driver stops anyway after the run ends,
// because the device is out of the operator's care.
//
// A device whose object a person deleted runs no deactivation. Its
// spec is gone, and keeping it until the run ends would need a
// finalizer on every device.

import (
	"context"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// leavers answers the devices of the pods that left a server whose
// objects still exist: the same object, by UID, with another parent or
// none. A pod whose device the step stops, with its parent unchanged,
// and the pod of a deleted device, are not leavers.
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
		r := t.ofDevice(d)
		r.telescope, r.observatory = "", ref.name
		if ref.kind == observatory.TelescopeKind {
			r.telescope, r.observatory = ref.name, ""
			if telescope, ok := t.telescopes[ref.name]; ok {
				r.observatory = telescope.Spec.Observatory
			}
		}
		out = append(out, r)
	}
	return out
}

// deactivateLeavers runs, in parallel, the deactivation of each device
// that left an Active server, and returns when every run has ended.
// Each action's timeout bounds its run.
func (o *operator) deactivateLeavers(ctx context.Context, t *tree, ref serverRef, pods []string) {
	key := telescopeKey(ref.name)
	if ref.kind == observatory.ObservatoryKind {
		key = observatoryKey(ref.name)
	}
	state := o.activity.get(key)
	if !state.active {
		return
	}
	var group sync.WaitGroup
	for _, r := range leavers(t, ref, pods) {
		if len(r.procedures.Deactivation) == 0 {
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
