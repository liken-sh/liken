package main

// A person can delete a device while a reservation is Ready. The
// deleted device leaves its server at once (tree.server), and the
// runner releases it as the reservation's release would:
//
//   - The device runs its deactivation through its driver on the
//     running server, while its parent is Active (leaves.go).
//   - The runner stops the device's driver on the running server
//     (serverdrivers.go), and posts an Event on the Telescope or the
//     Observatory. The other devices on the server stay connected.
//   - The device's own pod, Service, and claim carry the label of the
//     server, so the runner deletes them here. Without the delete, the
//     pod would run and the claim would hold the hardware until
//     deactivation's StopDevices.
//   - The runner switches off the output that the device's spec.power
//     names, as PowerOff would. Once the pod is gone, nothing places
//     the device on the server, so the reservation's PowerOff no longer
//     finds it. An output that another device still draws from stays
//     on (tree.draws).
//
// A device deleted while the reservation activates or is Failed keeps
// its pod until the reservation is Ready, or until the release.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// strayLimit bounds the wait for the pods of the deleted devices of a
// server to stop. It is a clock, the same deadline as StopDevices'.
const strayLimit = 2 * time.Minute

// reasonPodDeleted is the reason of the Event on a device whose pod a
// Ready reservation's runner deleted, because a person deleted the
// device.
const reasonPodDeleted = "PodDeleted"

// strays answers the device pods that carry a server's label but whose
// device the server no longer runs, because a person deleted it, as
// the pod's name by the device's key, such as Focuser/east.
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

// removeStrays deletes the pod, the Service, and the claim of each
// deleted device of a server, and posts an Event on each such device
// that still exists. A deleted device still exists while the finalizer
// holds it, and the supervisor removes the finalizer once its pod is
// gone (finalizers.go).
func (r *runner) removeStrays(ctx context.Context, t *tree, ref serverRef) error {
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
		d, ok := t.deviceByKey(key)
		if !ok {
			continue
		}
		message := fmt.Sprintf("Deleted pod %s, because the %s was deleted while Reservation %s is Ready.", left[key], d.kind.Name, r.name)
		notes, err := r.o.setOutputs(ctx, now, now.unpowered([]*device{d}, serverRef{}), false)
		if err != nil {
			r.o.logf("Reservation %s: switching off the output of %s: %v", r.name, key, err)
		}
		if len(notes) > 0 {
			message += " " + sentence(strings.Join(notes, "; ")) + "."
		}
		r.o.recorder.Normal(reference(d.kind, d.object.Metadata), reasonPodDeleted, message)
		r.o.logf("Reservation %s: deleted pod %s of %s, which was deleted", r.name, left[key], key)
	}
	return nil
}
