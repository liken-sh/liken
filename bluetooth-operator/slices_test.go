package main

// These tests prove that a write happens only when the devices
// changed, and that the slice is named for its node.

import "testing"

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and every
	// slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "a0-ab-51-33-b7-12"}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "b4-8c-9d-11-22-33"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different controller did not count as a change")
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if got := sliceName("liken-1"); got != "liken-1-bluetooth.liken.sh" {
		t.Fatalf("sliceName = %q", got)
	}
}
