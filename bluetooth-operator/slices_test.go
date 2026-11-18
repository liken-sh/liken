package main

import "testing"

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	published := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("sameDevices reports a change when only the stored taint timestamp differs, want no change")
	}
}

func TestSameDevicesReportsRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "a0-ab-51-33-b7-12"}}
	if sameDevices(tainted, clear) {
		t.Fatal("sameDevices reports no change after a taint is cleared, want a change")
	}
	renamed := []SliceDevice{{Name: "b4-8c-9d-11-22-33"}}
	if sameDevices(clear, renamed) {
		t.Fatal("sameDevices reports no change for a different device name, want a change")
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so this slice and the liken.sh
	// slice of the same node have different names.
	if got := sliceName("liken-1"); got != "liken-1-bluetooth.liken.sh" {
		t.Fatalf("sliceName(\"liken-1\") = %q, want \"liken-1-bluetooth.liken.sh\"", got)
	}
}
