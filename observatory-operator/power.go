package main

// A device's spec.power names a Switch and an output number, counting
// from 1. The operator turns the output on or off through the property
// DIGITAL_OUTPUT_<n> of the Switch's INDI device, whose members are OFF
// and ON.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// setOutputs turns the outputs of the devices on or off, and returns a
// note for each Switch it changed. A Switch that it cannot reach is an
// error when the outputs must turn on, and only a note when they must
// turn off.
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
		// Use the server that runs the Switch's pod, even when its spec
		// now names other parents, so a Switch that a person deleted or moved
		// during the session still turns its outputs off on its old
		// server when that server is released.
		ref, installed := t.runningOn(sw)
		if !installed {
			ref, installed = t.server(sw)
		}
		if !installed {
			if power {
				return nil, fmt.Errorf("the Switch %s, which spec.power names, is not installed: set its spec.telescope or spec.observatory", name)
			}
			notes = append(notes, fmt.Sprintf("left the outputs of Switch %s as they are: it is not installed", name))
			continue
		}
		handles, _ := o.connectedHandles(ctx, nil, t, ref, []*device{sw})
		if len(handles) == 0 {
			if power {
				return nil, fmt.Errorf("the Switch %s is not connected on server %s", name, ref)
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

// poweredBy returns the devices whose outputs the release of a server
// turns off: the devices that the release stops (releasedOn), and a
// device built from the recorded spec for each recorded device whose
// object is gone or has a new UID. The operator removes a deleted
// device's finalizer once its pod is gone, so after StopDevices only the
// record names the output that the session turned on for it.
func (t *tree) poweredBy(ref serverRef, released []*device) []*device {
	out := slices.Clone(released)
	for _, key := range sortedKeys(t.records[ref].Devices) {
		r := t.records[ref].Devices[key]
		if d, ok := t.deviceByKey(key); ok && sameUID(r.UID, d.object.Metadata) {
			continue
		}
		kindName, name, _ := strings.Cut(key, "/")
		kind, _ := observatory.KindNamed(kindName)
		gone := &device{kind: kind, record: &r, recordedOn: ref}
		gone.object.Metadata.Name = name
		gone.object.Spec.DeviceSpec = r.DeviceSpec
		out = append(out, gone)
	}
	return out
}

// unpowered returns the devices whose output no device in the tree
// still uses (draws), so that an output of an observatory's Switch
// that powers devices on two telescopes stays on while one of them
// runs. stopping is the server being released, whose devices do not
// count, or serverRef{} for none.
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

// draws reports whether a device keeps its power output in use: the
// tree places it on a server other than stopping, that server's pod exists
// and is not being deleted, and the device's pod is not being deleted. A device with no
// pod yet counts, because PowerOn turns its output on before
// StartDevices creates its pod. A device whose pod is being deleted
// does not count, so two releases that overlap do not each keep the
// other's outputs on.
func (t *tree) draws(d *device, stopping serverRef) bool {
	ref, placed := t.server(d)
	_, p, running := t.podOf(d)
	return placed && ref != stopping && t.serverRuns(ref) && !(running && p.Metadata.DeletionTimestamp != nil)
}

func (t *tree) serverRuns(ref serverRef) bool {
	p, ok := t.pods[ref.String()]
	return ok && p.Metadata.DeletionTimestamp == nil
}
