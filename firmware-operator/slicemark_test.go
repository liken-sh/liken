package main

import (
	"testing"

	resourceapi "k8s.io/api/resource/v1"
)

// slicePublishing builds a ResourceSlice with one device at the given
// device number and a fixed pool generation, as a writer that does not
// increase the generation would publish it.
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

// When a device enumerates again, the slice's devices change. The
// change must start a pass even when the slice's writer leaves the pool
// generation unchanged.
func TestASliceMarkChangesWithItsDevices(t *testing.T) {
	if sliceMark(slicePublishing(12)) == sliceMark(slicePublishing(13)) {
		t.Error("the mark did not change when the device number changed")
	}
}
