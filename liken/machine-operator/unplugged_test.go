package main

// These tests cover the record of a prepared claim whose device was
// unplugged, and the taint the slice publishes for it. Each test
// prepares the stick of newDRAFixture, changes sysfs the way the
// kernel does, and runs the refresh that the reconcile pass runs.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// unplug removes the stick and its USB device from sysfs, as the
// kernel does when the cable leaves the port.
func unplug(t *testing.T) {
	t.Helper()
	usb := filepath.Join(draSysfsRoot, "bus", "usb", "devices")
	for _, name := range []string{"2-1:1.0", "2-1"} {
		if err := os.RemoveAll(filepath.Join(usb, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// podOwner is the owner of a claim that a ResourceClaimTemplate made
// for its pod.
var podOwner = []map[string]any{{"apiVersion": "v1", "kind": "Pod", "name": "stick-0", "uid": "pod-1"}}

func TestAnUnpluggedDeviceIsTainted(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, fixture *draFixture)
	}{
		{"the cable leaves the port", func(t *testing.T, _ *draFixture) { unplug(t) }},
		{"the device enumerates again between two passes", func(t *testing.T, fixture *draFixture) { fixture.enumerate(t, 9) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDRAFixture(t)
			fixture.owner = podOwner
			fixture.enumerate(t, 4)
			prepared(t, fixture)

			c.change(t, fixture)
			refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

			if names := unpluggedDevices(); !slices.Equal(names, []string{"usb-2-1-1-0"}) {
				t.Errorf("unplugged = %v, want the stick", names)
			}
		})
	}
}

// Nothing that leaves the unit at its address marks the claim: a
// program that detaches the kernel driver, a pass with no change, and
// a spec that an operator wrote before it recorded units.
func TestAPresentDeviceIsNotTainted(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, fixture *draFixture)
	}{
		{"nothing changes", func(t *testing.T, _ *draFixture) {}},
		{"a program detaches the kernel driver", func(t *testing.T, _ *draFixture) {
			stick := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")
			if err := os.Remove(filepath.Join(stick, "driver")); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(stick, "host0")); err != nil {
				t.Fatal(err)
			}
		}},
		{"the walk cannot read the device number", func(t *testing.T, _ *draFixture) {
			if err := os.Remove(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1", "devnum")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDRAFixture(t)
			fixture.owner = podOwner
			fixture.enumerate(t, 4)
			prepared(t, fixture)

			c.change(t, fixture)
			refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

			if names := unpluggedDevices(); len(names) != 0 {
				t.Errorf("unplugged = %v, want none", names)
			}
		})
	}
}

func TestASpecWithNoUnitRecordIsNotTainted(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.owner = podOwner
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	forgetUnits(t, fixture, "claim-1")

	unplug(t)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	if names := unpluggedDevices(); len(names) != 0 {
		t.Errorf("unplugged = %v, want none", names)
	}
}

// forgetUnits rewrites a claim's spec the way an operator that did not
// record units wrote it.
func forgetUnits(t *testing.T, fixture *draFixture, claimUID string) {
	t.Helper()
	path := filepath.Join(fixture.cdi, "liken.sh-"+claimUID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for i := range spec.Devices {
		spec.Devices[i].Annotations = nil
	}
	if err := writeCDISpec(claimUID, spec.Devices); err != nil {
		t.Fatal(err)
	}
}

// The taint stays when the device comes back to its port, because the
// claim's running container still holds the nodes of the old
// enumeration. The spec still follows the device, so a pod that
// tolerates the taint keeps the behavior it had: its next container
// start receives the current nodes.
func TestTheTaintStaysWhenTheDeviceReturns(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.owner = podOwner
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	stick := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")
	aside := filepath.Join(t.TempDir(), "unplugged")

	if err := os.Rename(stick, aside); err != nil {
		t.Fatal(err)
	}
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})
	if err := os.Rename(aside, stick); err != nil {
		t.Fatal(err)
	}
	fixture.enumerate(t, 5)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	if names := unpluggedDevices(); !slices.Equal(names, []string{"usb-2-1-1-0"}) {
		t.Errorf("unplugged = %v, want the stick", names)
	}
	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/005", "/dev/sda"}) {
		t.Errorf("paths = %v, want the stick's current nodes", paths)
	}
}

func TestUnprepareEndsTheTaint(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.owner = podOwner
	fixture.enumerate(t, 4)
	prepared(t, fixture)
	unplug(t)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	unprepared(t, fixture, "claim-1")

	if names := unpluggedDevices(); len(names) != 0 {
		t.Errorf("unplugged = %v, want none after unprepare", names)
	}
}

func TestAnUnplugPostsOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDRAFixture(t)
		fixture.owner = podOwner
		fixture.enumerate(t, 4)
		prepared(t, fixture)
		_, recorder, recorded := recordingClient(t, http.NotFoundHandler())
		notes := machineEvents{recorder, machineReference(&machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", UID: "uid-1"}})}

		unplug(t)
		refreshCDISpecs(draSysfsRoot, nil, notes)
		refreshCDISpecs(draSysfsRoot, nil, notes)

		want := []string{"Warning DeviceUnplugged: device usb-2-1-1-0 of claim media/stick was unplugged, and liken.sh/unplugged evicts the pods that hold the claim"}
		if got := posted(recorded); !slices.Equal(got, want) {
			t.Errorf("events = %q, want %q", got, want)
		}
	})
}

func TestTheSliceTaintsEachUnpluggedDevice(t *testing.T) {
	present := kubernetes.SliceDevice{Name: "usb-1-2-1-0", Attributes: map[string]kubernetes.DeviceAttribute{"driver": kubernetes.AttrString("cp210x")}}
	taint := []kubernetes.DeviceTaint{{Key: unpluggedTaint, Effect: "NoExecute"}}
	cases := []struct {
		name      string
		unplugged []string
		want      []kubernetes.SliceDevice
	}{
		{"none", nil, []kubernetes.SliceDevice{present}},
		{"a device that is gone publishes its name alone", []string{"usb-1-4-1-0"}, []kubernetes.SliceDevice{
			present,
			{Name: "usb-1-4-1-0", Taints: taint},
		}},
		{"a device that returned keeps its attributes", []string{"usb-1-2-1-0"}, []kubernetes.SliceDevice{
			{Name: present.Name, Attributes: present.Attributes, Taints: taint},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := taintUnplugged([]kubernetes.SliceDevice{present}, c.unplugged)
			if !sameSliceDevices(got, c.want) {
				t.Errorf("devices = %+v, want %+v", got, c.want)
			}
		})
	}
}

func sameSliceDevices(a, b []kubernetes.SliceDevice) bool {
	raw := func(d []kubernetes.SliceDevice) string {
		out, _ := json.Marshal(d)
		return string(out)
	}
	return raw(a) == raw(b)
}

// A named claim outlives its pods, and a controller's next pod reserves
// it before the evicted pod releases it, so the claim is never
// allocated again and every new pod is evicted in turn. Only a claim
// that a pod owns is tainted.
func TestTheDeviceOfANamedClaimIsNotTainted(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	unplug(t)
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})

	if names := unpluggedDevices(); len(names) != 0 {
		t.Errorf("unplugged = %v, want none for a named claim", names)
	}
	if paths := specPaths(t, fixture, "claim-1"); !slices.Equal(paths, []string{deviceAbsentNode}) {
		t.Errorf("paths = %v, want the absent node alone", paths)
	}
}

// An unprepare ends the claim's taint, and the slice drops it only on
// the next pass, so the unprepare wakes the pass.
func TestUnprepareWakesThePass(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.owner = podOwner
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	unprepared(t, fixture, "claim-1")

	select {
	case <-fixture.released:
	default:
		t.Error("the unprepare sent no wake")
	}
}
