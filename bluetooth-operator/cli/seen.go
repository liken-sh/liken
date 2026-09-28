package main

// The pure half of the pairing flow: reading what the radio
// reported out of a PairingRequest, drawing the live list of devices,
// and resolving a person's pick to an address. Nothing here touches
// the terminal or the cluster, so a test drives each part with values.

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// One device the radio observed during a window, the
// shape a person reads to find the address to approve.
type seenDevice struct {
	Address   string
	Name      string
	FirstSeen string
}

// The phases a PairingRequest reports, mirrored from
// the operator's own constants.
const (
	phaseOpen    = "Open"
	phasePaired  = "Paired"
	phaseExpired = "Expired"
)

// seenFrom reads the observed devices, the phase, and
// the produced Peripheral out of a PairingRequest object.
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

// stringField reads one string out of an unstructured
// map, and returns an empty string for a missing or non-string value.
func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

// renderSeen draws the live list. The wording is a
// placeholder until the maintainer authors it.
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

// resolveSelection turns what a person typed into one
// of the observed addresses. A number picks by position in the list,
// and anything else is matched against an address as the list prints
// it, so a person can paste the address instead of counting.
func resolveSelection(input string, devices []seenDevice) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("no selection")
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
	return "", fmt.Errorf("no device %q in the list", trimmed)
}
