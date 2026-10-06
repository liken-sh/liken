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
		// A Switch belongs to a telescope or an observatory, never to a
		// train, so its server is always known.
		ref, _ := t.server(sw)
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
