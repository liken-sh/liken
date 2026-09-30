package main

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// stick is the recurring unclaimed device in these tests: the lab's
// QEMU USB stick, waiting for the usb_storage driver.
var stick = machine.UnclaimedDevice{
	Modalias:   "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
	Bus:        "usb",
	Name:       "QEMU QEMU USB HARDDRIVE",
	Class:      "mass-storage",
	Candidates: []string{"usb_storage", "uas"},
	Message:    "declare usb_storage or uas in spec.modules",
}

// fixtureModuleTree points the soft-dependency reader at a tree of the
// test's making and restores the real tree when the test ends. Without
// it, the advice would read the host's own module tree, and a test's
// expectation would depend on the machine that runs it.
func fixtureModuleTree(t *testing.T, base string) {
	t.Helper()
	old := softdepBase
	softdepBase = base
	t.Cleanup(func() { softdepBase = old })
}

func TestTransitionsNarrateANewGap(t *testing.T) {
	fixtureModuleTree(t, t.TempDir())
	lines := hardwareTransitions(nil, []machine.UnclaimedDevice{stick}, nil)
	want := "liken: hardware: unclaimed usb mass-storage device QEMU QEMU USB HARDDRIVE: declare usb_storage or uas in spec.modules"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

// nic is an unclaimed Realtek NIC: the case that needs realtek loaded
// before r8169, a soft dependency modules.dep does not record.
var nic = machine.UnclaimedDevice{
	Modalias:   "pci:v000010ECd00008168sv00sd00bc02sc00i00",
	Bus:        "pci",
	Name:       "RTL8111/8168/8411 PCI Express Gigabit Ethernet Controller",
	Class:      "ethernet",
	Candidates: []string{"r8169"},
	Message:    "declare r8169 in spec.modules",
}

func TestTransitionsNameTheSoftdepChain(t *testing.T) {
	fixtureModuleTree(t, softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	}))
	lines := hardwareTransitions(nil, []machine.UnclaimedDevice{nic}, nil)
	want := "liken: hardware: unclaimed pci ethernet device RTL8111/8168/8411 PCI Express Gigabit Ethernet Controller: declare realtek, then r8169 in spec.modules"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

func TestTransitionsNarrateAClaim(t *testing.T) {
	devices := []hardware.Device{{Bus: "usb", Modalias: stick.Modalias, Driver: "usb-storage"}}
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, nil, devices)
	want := "liken: hardware: QEMU QEMU USB HARDDRIVE is now driven by usb-storage"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

func TestTransitionsNarrateARemoval(t *testing.T) {
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, nil, nil)
	want := "liken: hardware: QEMU QEMU USB HARDDRIVE was removed"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

// noisyChannel sends signals continuously, faster than any quiet
// interval, until the test ends. This is the pattern of a node whose
// containers are starting and stopping constantly.
func noisyChannel(t *testing.T) chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		for {
			select {
			case <-t.Context().Done():
				return
			case ch <- struct{}{}:
			default:
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return ch
}

func TestSettleReturnsAtTheCeilingUnderConstantNoise(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		settle(t.Context(), noisyChannel(t), time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 5*time.Second {
			t.Errorf("settle returned after %s, want the 5s ceiling", elapsed)
		}
	})
}

func TestSettleReturnsAtQuietWhenTheStreamStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{}, 1)
		started := time.Now()
		settle(t.Context(), ch, time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Errorf("settle returned after %s, want the 1s quiet interval", elapsed)
		}
	})
}

func TestTransitionsAreQuietWhenNothingChanged(t *testing.T) {
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, []machine.UnclaimedDevice{stick}, nil)
	if lines != nil {
		t.Errorf("lines = %q, want none", lines)
	}
}
