package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// detach removes a device's driver symlink, as a libusb program does
// when it detaches the driver.
func (f *draFixture) detach(t *testing.T, bus, address string) {
	t.Helper()
	if err := os.Remove(filepath.Join(draSysfsRoot, "bus", bus, "devices", address, "driver")); err != nil {
		t.Fatalf("removing the driver symlink of %s: %v", address, err)
	}
}

// driversProbe creates the USB bus's drivers_probe file. The kernel
// makes it write-only, but here it is readable so that a test can
// check what was written.
func (f *draFixture) driversProbe(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(draSysfsRoot, "bus", "usb", "drivers_probe"), nil, 0o644); err != nil {
		t.Fatalf("creating drivers_probe: %v", err)
	}
}

// readProbe returns the address that was written to drivers_probe.
func readProbe(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(draSysfsRoot, "bus", "usb", "drivers_probe"))
	if err != nil {
		t.Fatalf("reading drivers_probe: %v", err)
	}
	return string(raw)
}

// leaveClaimSpec writes the CDI spec file that prepare writes for a
// claim.
func leaveClaimSpec(t *testing.T, claimUID, allocated string) {
	t.Helper()
	if err := writeCDISpec(claimUID, []cdiDevice{{Name: claimUID + "-" + allocated}}); err != nil {
		t.Fatalf("writing the CDI spec of claim %s: %v", claimUID, err)
	}
}

// unprepared calls unprepare and fails the test if the response has
// an error.
func unprepared(t *testing.T, fixture *draFixture, claimUID string) {
	t.Helper()
	resp, err := fixture.plugin.NodeUnprepareResources(t.Context(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: claimUID}},
	})
	if err != nil {
		t.Fatalf("calling NodeUnprepareResources: %v", err)
	}
	if result := resp.Claims[claimUID]; result == nil || result.Error != "" {
		t.Fatalf("the unprepare response for the claim is %+v, want no error", result)
	}
}

func TestUnprepareBindsADriverAgainToADetachedInterface(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")
	leaveClaimSpec(t, "claim-1", "usb-2-1-1-0")

	unprepared(t, fixture, "claim-1")

	if address := readProbe(t); address != "2-1:1.0" {
		t.Errorf("drivers_probe = %q, want the interface of the claim", address)
	}
	if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
		t.Error("the CDI spec file still exists, want it removed")
	}
}

func TestUnprepareBindsADriverAgainForACompanionName(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")
	leaveClaimSpec(t, "claim-1", "usb-2-1-1-0-i2c-dev")

	unprepared(t, fixture, "claim-1")

	if address := readProbe(t); address != "2-1:1.0" {
		t.Errorf("drivers_probe = %q, want the parent device's interface", address)
	}
}

func TestUnprepareLeavesAnInterfaceThatKeptItsDriverAlone(t *testing.T) {
	// The fixture's interface is still bound to usb-storage.
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	leaveClaimSpec(t, "claim-1", "usb-2-1-1-0")

	unprepared(t, fixture, "claim-1")

	if address := readProbe(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareLeavesADeviceOnAnotherBusAlone(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.driversProbe(t)
	fixture.detach(t, "pci", "0000:00:02.0")
	leaveClaimSpec(t, "claim-1", "pci-0000-00-02-0")

	unprepared(t, fixture, "claim-1")

	if address := readProbe(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareWithNoSpecFileWritesNothing(t *testing.T) {
	// The kubelet can repeat an unprepare, and the repeat finds no
	// spec file.
	fixture := newDRAFixture(t)
	fixture.driversProbe(t)
	fixture.detach(t, "usb", "2-1:1.0")

	unprepared(t, fixture, "claim-1")

	if address := readProbe(t); address != "" {
		t.Errorf("drivers_probe = %q, want no write", address)
	}
}

func TestUnprepareSucceedsWhenTheRepairFails(t *testing.T) {
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
			leaveClaimSpec(t, "claim-1", "usb-2-1-1-0")

			unprepared(t, fixture, "claim-1")

			if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
				t.Error("the CDI spec file still exists, want it removed")
			}
		})
	}
}

// A directory at the spec's path makes the read fail.
func TestUnprepareWithAnUnreadableSpecBindsNothing(t *testing.T) {
	isolatePass(t)
	if err := os.Mkdir(cdiSpecPath("claim-1"), 0o755); err != nil {
		t.Fatalf("creating a directory at the spec path: %v", err)
	}
	walked := false

	rebindClaimDevices("claim-1", func() map[string]hardware.Device { walked = true; return nil })

	if walked {
		t.Error("sysfs was walked for a spec that could not be read, want no walk")
	}
}
