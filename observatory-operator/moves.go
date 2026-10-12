package main

// A device can leave a server while a reservation is Ready: a person
// removes its parent field to put it on the shelf, names another
// train, telescope, or observatory, or deletes the device. The runner
// handles the change as it handles a device that joins:
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
//   - The runner switches off the output that the device's spec.power
//     names, as PowerOff would. Once the pod is gone, nothing places
//     the device on the server, so the reservation's PowerOff no longer
//     finds it. An output that another device still draws from stays
//     on (tree.draws).
//
// A device that moves to another running server joins it only after
// its pod here is gone (tree.heldElsewhere), so the two servers never
// run one device's driver at once. The other server waits for this
// one, so the runner releases a device that left whatever its phase:
// while the reservation is Ready, while it is Failed, and between two
// activation steps (releaseLeft). A step that runs does not release a
// device that left during it, because the step may act on the server's
// drivers, so the device waits for the step to end.
//
// The runner refuses no change.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
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
		why := fmt.Sprintf("left %s %s", ref.kind.Name, ref.name)
		if deleting(d.object.Metadata) {
			why = "was deleted"
		}
		message := fmt.Sprintf("Deleted pod %s, because the %s %s while Reservation %s is %s.", left[key], d.kind.Name, why, r.name, r.status.Phase)
		notes, err := r.o.setOutputs(ctx, now, now.unpowered([]*device{d}, serverRef{}), false)
		if err != nil {
			r.o.logf("Reservation %s: switching off the output of %s: %v", r.name, key, err)
		}
		if len(notes) > 0 {
			message += " " + sentence(strings.Join(notes, "; ")) + "."
		}
		r.o.recorder.Normal(reference(d.kind, d.object.Metadata), reasonPodDeleted, message)
		r.o.logf("Reservation %s: deleted pod %s of %s, which left %s", r.name, left[key], key, ref)
	}
	return nil
}

// releaseLeft releases the devices that left the telescope's server or
// the observatory's server while the reservation is not Ready, as
// keepServer does while it is Ready, and starts nothing: each such
// device runs its deactivation, its driver stops, and its pod goes
// (releaseFrom).
func (r *runner) releaseLeft(ctx context.Context) {
	t := r.o.snapshot()
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return
	}
	ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
	if err := r.releaseFrom(ctx, t, ref, telescope.Metadata); err != nil {
		r.o.logf("releasing the devices that left %s: %v", ref, err)
	}
	site, ok := t.observatories[telescope.Spec.Observatory]
	if !ok {
		return
	}
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return
	}
	defer lock.release()
	ref = serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	if err := r.releaseFrom(ctx, r.o.snapshot(), ref, site.Metadata); err != nil {
		r.o.logf("releasing the devices that left %s: %v", ref, err)
	}
}

// releaseFrom stops the driver of each device that left a running
// server and deletes its pod. The server keeps the drivers it runs of
// the devices still on it, and starts none: a device that joined gets
// its driver from the steps or from a Ready runner, after its pod
// exists.
func (r *runner) releaseFrom(ctx context.Context, t *tree, ref serverRef, owner observatory.ObjectMeta) error {
	running, ok := t.pods[ref.String()]
	if !ok || running.Metadata.DeletionTimestamp != nil {
		return nil
	}
	listed := strings.Fields(r.o.serverDrivers.current(running))
	kept := slices.DeleteFunc(t.devicesOn(ref), func(d *device) bool {
		name, err := objectName(d.kind, d.name())
		return d.runsDriver() && (err != nil || !slices.Contains(listed, name+":"+strconv.Itoa(devicePort)))
	})
	if err := r.leaveServed(ctx, t, ref); err != nil {
		return err
	}
	_, left, err := r.o.setDrivers(ctx, nil, ref, kept)
	r.recordDrivers(t, ref, reference(ref.kind, owner), nil, left)
	if err != nil {
		return err
	}
	return r.removeStrays(ctx, t, ref)
}
