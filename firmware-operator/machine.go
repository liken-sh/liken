package main

// spec.machines limits a Firmware to the machines whose labels it
// selects. A Machine has the name of its node, so the nodeName of a
// slice names the Machine that the slice describes.

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type machine struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels,omitempty"`
	} `json:"metadata"`
}

// machineFilter answers whether a Firmware applies on a machine.
type machineFilter func(machine string) bool

// filterMachines builds the filter of spec.machines from the labels of
// each Machine. With no spec.machines the rule applies on every
// machine. A machine with no Machine object has no labels to match, so
// a rule with spec.machines does not apply there.
func filterMachines(selector *metav1.LabelSelector, machineLabels map[string]map[string]string) (machineFilter, error) {
	if selector == nil {
		return func(string) bool { return true }, nil
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("spec.machines: %w", err)
	}
	return func(name string) bool {
		held, exists := machineLabels[name]
		return exists && parsed.Matches(labels.Set(held))
	}, nil
}
