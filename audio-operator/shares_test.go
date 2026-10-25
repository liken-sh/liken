package main

// These tests cover the slice side of a shared endpoint: the share
// count a cluster owner declares on a Sink or a Source, and the
// capacity the published device carries for it.

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func intPointer(i int) *int { return &i }

// A device publishes allowMultipleAllocations and its claims capacity
// only when its resource declares a share count. Every other device
// publishes neither, so it allocates once, as it always has.
func TestShareDevicesPublishesTheDeclaredCount(t *testing.T) {
	cases := []struct {
		name   string
		shares map[string]int
		want   SliceDevice
	}{
		{
			name:   "no declaration",
			shares: map[string]int{},
			want:   SliceDevice{Name: testAnalogName},
		},
		{
			name:   "a declaration for another device",
			shares: map[string]int{testSpeakerName: 4},
			want:   SliceDevice{Name: testAnalogName},
		},
		{
			name:   "four claims",
			shares: map[string]int{testAnalogName: 4},
			want: SliceDevice{
				Name:                     testAnalogName,
				AllowMultipleAllocations: boolPointer(true),
				Capacity:                 map[string]DeviceCapacity{sharedClaimsCapacity: {Value: "4"}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shareDevices([]SliceDevice{{Name: testAnalogName}}, c.shares)
			if !reflect.DeepEqual(got, []SliceDevice{c.want}) {
				t.Errorf("devices = %+v, want %+v", got, c.want)
			}
		})
	}
}

// A count of one is no share. The schema refuses it, and the slice
// publishes the device as exclusive if a count below two reaches it
// anyway.
func TestShareDevicesIgnoresACountBelowTwo(t *testing.T) {
	got := shareDevices([]SliceDevice{{Name: testAnalogName}}, map[string]int{testAnalogName: 1})
	if !reflect.DeepEqual(got, []SliceDevice{{Name: testAnalogName}}) {
		t.Errorf("devices = %+v, want the device unshared", got)
	}
}

func boolPointer(b bool) *bool { return &b }

// The share counts come from the machine's Sinks and Sources, by the
// endpoint's device name. A resource that declares none is not in
// the answer.
func TestSharedClaimsReadsTheDeclaredCounts(t *testing.T) {
	api := newEndpointAPI()
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{SharedClaims: intPointer(4)},
		Status:   SinkStatus{EndpointStatus: EndpointStatus{Node: "liken-1"}},
	}
	api.sinks[testSpeakerName] = &Sink{
		Metadata: EndpointMeta{Name: testSpeakerName},
		Status:   SinkStatus{EndpointStatus: EndpointStatus{Node: "liken-1"}},
	}
	api.sources[testSourceName] = &Source{
		Metadata: EndpointMeta{Name: testSourceName},
		Spec:     SourceSpec{SharedClaims: intPointer(2)},
		Status:   EndpointStatus{Node: "liken-1"},
	}
	control := testEndpointControl(t, api, &writeRecord{})

	shares, err := control.sharedClaims()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{testAnalogName: 4, testSourceName: 2}
	if !reflect.DeepEqual(shares, want) {
		t.Errorf("shares = %v, want %v", shares, want)
	}
}

// The pass publishes the declared share in the slice it writes, and a
// later pass that cannot read the resources keeps the share it read
// last. A share that vanished on one failed read would publish the
// device as exclusive for a pass.
func TestReconcilePublishesTheDeclaredShare(t *testing.T) {
	slices := &slicePublishFixture{}
	operator := testReconciler(t, slices, staticGraph(outputGraph(map[pcmAddress]string{
		{Card: 0, PCM: 0}: sinkNodeName(0, 0),
	})), "pcmC0D0p")
	read := map[string]int{testAnalogName: 3}
	operator.readShares = func() (map[string]int, error) { return read, nil }

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertShared(t, lastPublished(t, slices), testAnalogName, "3")

	operator.readShares = func() (map[string]int, error) { return nil, errors.New("the API server refused the list") }
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertShared(t, lastPublished(t, slices), testAnalogName, "3")
}

// lastPublished is the slice the fixture received last.
func lastPublished(t *testing.T, f *slicePublishFixture) []SliceDevice {
	t.Helper()
	if f.updated != nil {
		return f.updated.Spec.Devices
	}
	if f.created == nil {
		t.Fatal("the pass published nothing")
	}
	return f.created.Spec.Devices
}

// assertShared fails the test unless the slice publishes the named
// device as shared by the given number of claims.
func assertShared(t *testing.T, devices []SliceDevice, name, claims string) {
	t.Helper()
	for _, device := range devices {
		if device.Name != name {
			continue
		}
		if device.AllowMultipleAllocations == nil || !*device.AllowMultipleAllocations {
			t.Errorf("%s is published without allowMultipleAllocations: %+v", name, device)
		}
		if got := device.Capacity[sharedClaimsCapacity].Value; got != claims {
			t.Errorf("%s publishes %q claims, want %q", name, got, claims)
		}
		return
	}
	t.Errorf("the slice holds no device %s: %+v", name, devices)
}
