package main

// The Switch outputs that power the devices. A device's spec.power
// names a Switch and an output, from output 1, and the operator turns
// the output on through the Switch's INDI device as DIGITAL_OUTPUT_<n>,
// whose members are OFF and ON. The Switch can be on the telescope's
// server or on the observatory's.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// setOutputs turns the outputs that the devices name on or off, and
// answers what it changed. A Switch whose server does not run cannot be
// reached, and setOutputs answers an error for it when the outputs must
// turn on, and a note when they must turn off.
func (o *operator) setOutputs(ctx context.Context, t *tree, devices []*device, power bool) ([]string, error) {
	member, verb := "OFF", "off"
	if power {
		member, verb = "ON", "on"
	}
	var notes []string
	byswitch := outputs(devices)
	for _, name := range sortedKeys(byswitch) {
		sw, ok := t.device(observatory.SwitchKind, name)
		if !ok {
			if power {
				return nil, fmt.Errorf("missing Switch %s, which spec.power names", name)
			}
			continue
		}
		// A Switch runs on the server that holds its pod until the pod
		// is gone (tree.heldElsewhere), and is reached there, whatever
		// its parents say now: a Switch that a person deleted or moved
		// switches its outputs off on the server it left, during that
		// server's release. A Switch with no pod is reached on the
		// server that the tree places it on, which is known unless it is
		// on the shelf, because a Switch never belongs to a train.
		ref, installed := t.runningOn(sw)
		if !installed {
			ref, installed = t.server(sw)
		}
		if !installed {
			if power {
				return nil, fmt.Errorf("the Switch %s, which spec.power names, is on the shelf: set its spec.telescope or spec.observatory", name)
			}
			notes = append(notes, fmt.Sprintf("left the outputs of Switch %s as they are: it is on the shelf", name))
			continue
		}
		handles, _ := o.connectedHandles(ctx, nil, t, ref, []*device{sw})
		if len(handles) == 0 {
			if power {
				return nil, fmt.Errorf("Switch %s not connected on %s", name, ref)
			}
			notes = append(notes, fmt.Sprintf("left the outputs of Switch %s as they are: not connected", name))
			continue
		}
		h := handles[0]
		var changed []string
		for _, output := range byswitch[name] {
			property := "DIGITAL_OUTPUT_" + strconv.Itoa(int(output))
			sent, err := h.switchOn(ctx, property, member)
			if err != nil {
				return nil, err
			}
			if sent {
				changed = append(changed, strconv.Itoa(int(output)))
			}
		}
		if len(changed) > 0 {
			notes = append(notes, fmt.Sprintf("switched %s output %s of Switch %s", verb, strings.Join(changed, ", "), name))
		}
	}
	return notes, nil
}

// unpowered answers the devices whose output no device that draws
// from it names (draws), apart from the devices of the server that
// stops: the server whose release switches the outputs off, or none
// (serverRef{}). A device that leaves, or a server that releases, so
// leaves on an output that another device still draws from, such as an
// output of an observatory's Switch that powers devices on two
// telescopes.
func (t *tree) unpowered(devices []*device, stopping serverRef) []*device {
	used := map[observatory.Power]bool{}
	for _, d := range t.devices {
		if p := d.object.Spec.Power; p != nil && t.draws(d, stopping) {
			used[*p] = true
		}
	}
	var out []*device
	for _, d := range devices {
		if p := d.object.Spec.Power; p != nil && !used[*p] {
			out = append(out, d)
		}
	}
	return out
}

// draws reports whether a device draws from its output on a server
// other than the one that stops. A device draws while the pod of its
// driver runs, and is not stopping, on a running server. A device with
// no pod draws when the tree places it on a running server that a
// reservation activates or holds Ready, because PowerOn and
// powerJoining switch its output on before its pod starts. Once a
// release's StopDevices has deleted the pods, its devices draw nothing,
// so two releases that overlap do not each leave the other's output
// on.
func (t *tree) draws(d *device, stopping serverRef) bool {
	if _, p, ok := t.podOf(d); ok {
		on, known := t.serverNamed(p.Metadata.Labels[labelServer])
		return known && on != stopping && p.Metadata.DeletionTimestamp == nil && t.serverRuns(on)
	}
	ref, placed := t.server(d)
	return placed && ref != stopping && t.serverRuns(ref) && t.inSession(ref)
}

// serverRuns reports whether a server's pod exists and is not stopping.
func (t *tree) serverRuns(ref serverRef) bool {
	p, ok := t.pods[ref.String()]
	return ok && p.Metadata.DeletionTimestamp == nil
}

// inSession reports whether a reservation that runs a server activates
// or is Ready: the reservation of the telescope, or of any telescope in
// the observatory.
func (t *tree) inSession(ref serverRef) bool {
	for _, res := range t.reservations {
		telescope, ok := t.telescopes[res.Spec.Telescope]
		runs := ok && (ref == serverRef{observatory.TelescopeKind, telescope.Metadata.Name} ||
			ref == serverRef{observatory.ObservatoryKind, telescope.Spec.Observatory})
		if phase := res.Status.Phase; runs && (phase == observatory.ReservationActivating || phase == observatory.ReservationReady) {
			return true
		}
	}
	return false
}

// powerJoining switches on the output that each device without a pod
// on a running server names, before the runner creates the pod, as
// PowerOn does before StartDevices. A device that joins the server
// while the reservation is Ready can name an output that is off: the
// output of a camera that left switched off with it. A driver of real
// hardware connects only to a device with power, and a pod with a claim
// stays Pending until its device appears. powerJoining answers what it
// switched for each device, by the device's key, for the Event on the
// device's new pod.
//
// A device whose pod is gone for another reason, such as an eviction,
// names an output that is on already, and powerJoining sends nothing
// for it. A failure does not stop the pod: the Switch can be the device
// that is coming back, and reapply switches every output on again when
// the Switch's driver comes back.
func (r *runner) powerJoining(ctx context.Context, t *tree, ref serverRef, devices []*device) map[string]string {
	out := map[string]string{}
	for _, d := range driverDevices(devices) {
		name, err := objectName(d.kind, d.name())
		if _, running := t.pods[name]; err != nil || running || d.object.Spec.Power == nil {
			continue
		}
		notes, err := r.o.setOutputs(ctx, t, []*device{d}, true)
		if err != nil {
			r.o.logf("Reservation %s: switching on the output of %s, which joins %s: %v", r.name, d.key(), ref, err)
			continue
		}
		if len(notes) > 0 {
			out[d.key()] = sentence(strings.Join(notes, "; ")) + "."
		}
	}
	return out
}
