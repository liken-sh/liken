package main

// When a person deletes a device while a reservation is Ready, the
// device leaves its server at once (tree.server), and the runner
// releases it the way the reservation's release would:
//
//   - The device runs its deactivation through its driver, while its
//     parent is Active (leaves.go).
//   - The runner stops the device's driver on the running server
//     (serverdrivers.go) and posts an Event on the Telescope or the
//     Observatory. The other devices on the server stay connected.
//   - The runner deletes the device's pod, Service, and claim. Without
//     this, the pod would run and the claim would hold the hardware
//     until the StopDevices step of deactivation.
//   - The runner switches off the output in the device's spec.power,
//     unless another device still draws from it (tree.draws). The
//     PowerOff step cannot do it, because once the pod is gone nothing
//     places the device on the server, and PowerOff does not find it.
//
// A device that a retry's new record (retryrecords.go) leaves out
// leaves the session the same way, at the retry. A device deleted while
// the reservation is activating or Failed keeps its pod until the
// retry, until the reservation is Ready, or until the release.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// strayLimit is the deadline for the pods of devices that left a server
// to stop. It equals the deadline of the StopDevices step.
const strayLimit = 2 * time.Minute

// reasonPodDeleted is the reason of the Event that removeStrays posts on
// a device when it deletes the device's pod.
const reasonPodDeleted = "PodDeleted"

// strays returns the device pods that have a server's label although
// the server no longer runs their device, because a person deleted it
// or a retry's new record left it out. It maps each device key, such
// as Focuser/east, to the pod's name.
func strays(t *tree, ref serverRef) map[string]string {
	on := map[string]bool{}
	for _, d := range t.devicesOn(ref) {
		on[d.key()] = true
	}
	out := map[string]string{}
	for name, p := range t.pods {
		l := p.Metadata.Labels
		if key := l[labelKind] + "/" + l[labelResource]; l[labelRole] == roleDevice && l[labelServer] == ref.String() && !on[key] {
			out[key] = name
		}
	}
	return out
}

// removeStrays deletes the pod, Service, and claim of each device that
// a server no longer runs, switches off the device's output, and posts
// an Event on the device. A deleted device exists until the supervisor
// removes its finalizer, after its pod is gone (finalizers.go). from is
// the tree that placed the device on the server, whose spec of the
// device has the output that the session switched on: t itself after a
// delete, and the tree of the old record after a retry.
func (r *runner) removeStrays(ctx context.Context, t, from *tree, ref serverRef) error {
	left := strays(t, ref)
	if len(left) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, strayLimit)
	defer cancel()
	_, err := r.o.stopPods(ctx, nil, func(l map[string]string) bool {
		_, stray := left[l[labelKind]+"/"+l[labelResource]]
		return stray && l[labelRole] == roleDevice && l[labelServer] == ref.String()
	})
	if err != nil {
		return err
	}
	now := r.o.snapshot()
	for _, key := range slices.Sorted(maps.Keys(left)) {
		d, ok := from.deviceByKey(key)
		if !ok {
			continue
		}
		because := fmt.Sprintf("the %s was deleted while Reservation %s is %s", d.kind.Name, r.name, r.status.Phase)
		if !deleting(d.object.Metadata) {
			because = fmt.Sprintf("the retry of Reservation %s recorded a spec that takes the %s out of its session", r.name, d.kind.Name)
		}
		message := fmt.Sprintf("Deleted pod %s because %s", left[key], because)
		notes, err := r.o.setOutputs(ctx, now, now.unpowered([]*device{d}, serverRef{}), false)
		if err != nil {
			r.o.logf("Reservation %s: switching off the output of %s: %v", r.name, key, err)
		}
		if len(notes) > 0 {
			message += ". " + sentence(strings.Join(notes, ", "))
		}
		r.o.recorder.Normal(reference(d.kind, d.object.Metadata), reasonPodDeleted, message)
		r.o.logf("Reservation %s: deleted pod %s of %s because %s", r.name, left[key], key, because)
	}
	return nil
}
