package main

import (
	"testing"

	resourceapi "k8s.io/api/resource/v1"
)

// slicePublishing builds a slice of one device at a device number,
// with the pool generation fixed, the way a writer that does not move
// the generation would publish it.
func slicePublishing(deviceNumber int64) resourceSlice {
	var s resourceSlice
	s.Metadata.UID = "slice-uid"
	s.Spec.Pool.Generation = 1
	s.Spec.Devices = []sliceDevice{{
		Name:       "usb-1-4-1-2",
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{"deviceNumber": {IntValue: &deviceNumber}},
	}}
	return s
}

// A device that enumerates again changes the slice's devices, and the
// change wakes a pass even when the slice's writer leaves the pool's
// generation as it was.
func TestASliceMarkChangesWithItsDevices(t *testing.T) {
	if sliceMark(slicePublishing(12)) == sliceMark(slicePublishing(13)) {
		t.Error("the mark did not change when the device number changed")
	}
}
