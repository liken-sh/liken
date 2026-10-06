package main

// A device can leave a server while a reservation is Ready: a person
// removes its parent field to put it on the shelf, or names another
// train, telescope, or observatory. The runner handles the change as
// it handles a device that joins:
//
//   - The device runs its deactivation through its driver on the
//     running server, while its old parent is Active (leaves.go).
//   - The runner stops the device's driver on the running server
//     (serverdrivers.go), and posts an Event on the Telescope or the
//     Observatory. The other devices on the server stay connected.
//   - The device's own pod, Service, and claim carry the label of the
//     server it left, so the runner deletes them here. Without the
//     delete, the pod would run and the claim would hold the hardware
//     until deactivation's StopDevices.
//
// The runner refuses no change.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"
)

// strayLimit bounds the wait for the pods of the devices that left a
// server to stop. It is a clock, the same deadline as StopDevices'.
const strayLimit = 2 * time.Minute

// reasonPodDeleted is the reason of the Event on a device whose pod a
// Ready reservation's runner deleted, because the device left the
// server.
const reasonPodDeleted = "PodDeleted"

// strays answers the device pods that carry a server's label but whose
// device is no longer on the server, as the pod's name by the device's
// key, such as Focuser/east.
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
// device that left a server, and posts an Event on each such device
// that still exists. A deleted device's pod goes by garbage collection
// too, and has no device to post on.
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
	for _, key := range slices.Sorted(maps.Keys(left)) {
		d, ok := t.deviceByKey(key)
		if !ok {
			continue
		}
		r.o.recorder.Normal(reference(d.kind, d.object.Metadata), reasonPodDeleted,
			fmt.Sprintf("Deleted pod %s, because the %s left %s %s while Reservation %s is Ready.", left[key], d.kind.Name, ref.kind.Name, ref.name, r.name))
		r.o.logf("Reservation %s: deleted pod %s of %s, which left %s", r.name, left[key], key, ref)
	}
	return nil
}
