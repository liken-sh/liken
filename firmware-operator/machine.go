package main

// A Machine has the same name as its node, so the nodeName of a
// ResourceSlice is the name of its Machine. The operator scans only a
// Ready machine, and scans it again after each boot, because a firmware
// version changes only across a reboot or a flash.

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
	Status struct {
		Phase string `json:"phase,omitempty"`
		Boot  struct {
			Time string `json:"time,omitempty"`
		} `json:"boot"`
		Firmware struct {
			BootOrderKept *bool `json:"bootOrderKept,omitempty"`
		} `json:"firmware"`
	} `json:"status"`
}

// hasNextESP reports whether init has linked the next boot's EFI system
// partition at /run/liken/proven-esp. init links it only on a machine
// that records a proven slot, and only such a machine reports
// status.firmware.bootOrderKept.
func (m machine) hasNextESP() bool { return m.Status.Firmware.BootOrderKept != nil }

const machineReady = "Ready"

type machineFilter func(machine string) bool

// filterMachines builds the filter for spec.machines. A Firmware with
// spec.machines does not apply on a node that has no Machine.
func filterMachines(selector *metav1.LabelSelector, machineLabels map[string]map[string]string) (machineFilter, error) {
	if selector == nil {
		return func(string) bool { return true }, nil
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("spec.machines: %w", err)
	}
	return func(name string) bool {
		set, exists := machineLabels[name]
		return exists && parsed.Matches(labels.Set(set))
	}, nil
}
