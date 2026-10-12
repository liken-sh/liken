package main

// The Switch outputs that power the devices. A device's spec.power
// names a Switch and an output, from output 1, and the operator turns
// the output on through the Switch's INDI device as DIGITAL_OUTPUT_<n>,
// whose members are OFF and ON. The Switch can be on the telescope's
// server or on the observatory's.

import (
	"context"
	"fmt"
	"slices"
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
		// A Switch is reached on the server that its pod runs on,
		// whatever its parents say now: a Switch that a person deleted,
		// or moved during the session, switches its outputs off there
		// during that server's release. A Switch with no pod is reached
		// on the server that the tree places it on, which is known
		// unless it is on the shelf, because a Switch never belongs to a
		// train.
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

// poweredBy answers the devices whose outputs the release of a
// server switches off: the devices that it releases (releasedOn), and
// one for each device in the server's record whose object is gone or
// was created again, with the recorded spec. The finalizer holds a
// deleted device only until its pod is gone, so after StopDevices only
// the record names the output that the session switched on for it.
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

// unpowered answers the devices whose output no device that draws
// from it names (draws), apart from the devices of the server that
// stops: the server whose release switches the outputs off, or none
// (serverRef{}). A deleted device, or a server that releases, so
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
// other than the one that stops: the tree places it on a running
// server (tree.server), and its pod is not stopping. A device with no
// pod draws there too, because PowerOn switches its output on before
// StartDevices creates its pod. Once a release's StopDevices has
// deleted the pods, the session's set no longer holds its devices, so
// two releases that overlap do not each leave the other's output on.
func (t *tree) draws(d *device, stopping serverRef) bool {
	ref, placed := t.server(d)
	_, p, running := t.podOf(d)
	return placed && ref != stopping && t.serverRuns(ref) && !(running && p.Metadata.DeletionTimestamp != nil)
}

// serverRuns reports whether a server's pod exists and is not stopping.
func (t *tree) serverRuns(ref serverRef) bool {
	p, ok := t.pods[ref.String()]
	return ok && p.Metadata.DeletionTimestamp == nil
}
