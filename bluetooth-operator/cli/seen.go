package main

// The parts of the pairing flow that need no terminal or cluster:
// reading the devices, the phase, and the Peripheral from a
// PairingRequest, rendering the list of devices, and resolving what a
// person typed to an address.

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// seenDevice is one device from status.seen of a PairingRequest.
type seenDevice struct {
	Address   string
	Name      string
	FirstSeen string
}

// The values of status.phase of a PairingRequest. They are copies of
// the operator's values, because the CLI does not import the operator.
const (
	phaseOpen    = "Open"
	phasePaired  = "Paired"
	phaseExpired = "Expired"
)

// seenFrom returns status.seen, status.phase, and status.peripheral of
// a PairingRequest.
func seenFrom(object *unstructured.Unstructured) ([]seenDevice, string, string) {
	phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
	peripheral, _, _ := unstructured.NestedString(object.Object, "status", "peripheral")
	raw, _, _ := unstructured.NestedSlice(object.Object, "status", "seen")
	devices := make([]seenDevice, 0, len(raw))
	for _, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		devices = append(devices, seenDevice{
			Address:   stringField(fields, "address"),
			Name:      stringField(fields, "name"),
			FirstSeen: stringField(fields, "firstSeen"),
		})
	}
	return devices, phase, peripheral
}

// stringField returns fields[key], or "" when it is not a string.
func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

// renderSeen returns the numbered list of devices, one per line.
func renderSeen(devices []seenDevice) string {
	if len(devices) == 0 {
		return "no devices yet\n"
	}
	var builder strings.Builder
	for index, device := range devices {
		name := device.Name
		if name == "" {
			name = "unnamed"
		}
		fmt.Fprintf(&builder, "%d) %s %s\n", index+1, device.Address, name)
	}
	return builder.String()
}

// resolveSelection returns the address of the device that input picks.
// A number picks by its position in the list, counting from 1. Any
// other input must match an address in the list, ignoring case.
func resolveSelection(input string, devices []seenDevice) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("the selection is empty")
	}
	if index, err := strconv.Atoi(trimmed); err == nil {
		if index < 1 || index > len(devices) {
			return "", fmt.Errorf("no device %d in the list", index)
		}
		return devices[index-1].Address, nil
	}
	for _, device := range devices {
		if strings.EqualFold(device.Address, trimmed) {
			return device.Address, nil
		}
	}
	return "", fmt.Errorf("no device in the list has the address %q", trimmed)
}
