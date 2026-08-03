package main

// A libusb program in a pod leaves its interface with no driver. The
// end of the claim must give the interface back to the kernel, and it
// must do that without ever failing the unprepare call.

import (
	"os"
	"path/filepath"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// detach removes a device's driver symlink. This is what sysfs shows
// after a libusb program claims the interface and stops.
func (f *draFixture) detach(t *testing.T, bus, address string) {
	t.Helper()
	if err := os.Remove(filepath.Join(draSysfsRoot, "bus", bus, "devices", address, "driver")); err != nil {
		t.Fatal(err)
	}
}

// driversProbe creates the USB bus attribute the repair writes to. On
// a real machine the kernel creates it with the bus, write-only. Here
// it is readable, so a test can check what the repair wrote.
func (f *draFixture) driversProbe(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(draSysfsRoot, "bus", "usb", "drivers_probe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// probed reads back the address the repair wrote.
func probed(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(draSysfsRoot, "bus", "usb", "drivers_probe"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// held writes the spec file a prepare would leave for one claim. The
// file is the only record unprepare has of what the claim held.
func held(t *testing.T, claimUID, allocated string) {
	t.Helper()
	if err := writeCDISpec(claimUID, []cdiDevice{{Name: claimUID + "-" + allocated}}); err != nil {
		t.Fatal(err)
	}
}

// unprepared runs one unprepare call and requires that it succeeds.
func unprepared(t *testing.T, fixture *draFixture, claimUID string) {
	t.Helper()
	resp, err := fixture.plugin.NodeUnprepareResources(t.Context(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: claimUID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims[claimUID]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestUnprepareBindsADriverAgainToADetachedInterface(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")
	held(t, "claim-1", "usb-2-1-1-0")

	unprepared(t, fixture, "claim-1")

	if address := probed(t); address != "2-1:1.0" {
		t.Errorf("drivers_probe = %q, want the interface the claim held", address)
	}
	if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
		t.Error("the spec file must be gone")
	}
}

func TestUnprepareBindsADriverAgainForACompanionName(t *testing.T) {
	// A companion is one wire of the same physical device, so its name
	// names the same interface, and that interface is what the repair
	// gives back.
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")
	held(t, "claim-1", "usb-2-1-1-0-i2c-dev")

	unprepared(t, fixture, "claim-1")

	if address := probed(t); address != "2-1:1.0" {
		t.Errorf("drivers_probe = %q, want the parent device's interface", address)
	}
}

func TestUnprepareLeavesAnInterfaceThatKeptItsDriverAlone(t *testing.T) {
	// The pod never detached the driver. The interface still holds
	// usb-storage, so the repair must not write.
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	held(t, "claim-1", "usb-2-1-1-0")

	unprepared(t, fixture, "claim-1")

	if address := probed(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareLeavesADeviceOnAnotherBusAlone(t *testing.T) {
	// A PCI function with no driver is a different case. It binds
	// through its own bus, and the USB attribute names nothing that
	// could match it.
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.driversProbe(t)
	fixture.detach(t, "pci", "0000:00:02.0")
	held(t, "claim-1", "pci-0000-00-02-0")

	unprepared(t, fixture, "claim-1")

	if address := probed(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareWithNoSpecFileWritesNothing(t *testing.T) {
	// The kubelet repeats an unprepare it is not sure of. The second
	// call has no record to act on, and it must not walk sysfs or write
	// anything.
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")

	unprepared(t, fixture, "claim-1")

	if address := probed(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareSucceedsWhenTheRepairFails(t *testing.T) {
	// A device that keeps no driver is a problem for the next pod that
	// claims it. It must never block the deletion of a pod that has
	// already stopped.
	cases := map[string]func(*testing.T, *draFixture){
		"the tree has no drivers_probe": func(t *testing.T, fixture *draFixture) {
			fixture.detach(t, "usb", "2-1:1.0")
		},
		"the hardware left the machine": func(t *testing.T, fixture *draFixture) {
			fixture.driversProbe(t)
			if err := os.RemoveAll(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newDRAFixture(t)
			setup(t, fixture)
			held(t, "claim-1", "usb-2-1-1-0")

			unprepared(t, fixture, "claim-1")

			if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
				t.Error("the spec file must be gone")
			}
		})
	}
}
