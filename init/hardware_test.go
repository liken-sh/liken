package main

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// stick is the recurring unclaimed device in these tests: the lab's
// QEMU USB stick, waiting on usb_storage.
var stick = machine.UnclaimedDevice{
	Modalias:   "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
	Bus:        "usb",
	Name:       "QEMU QEMU USB HARDDRIVE",
	Class:      "mass-storage",
	Candidates: []string{"usb_storage", "uas"},
	Message:    "declare usb_storage or uas in spec.modules",
}

func TestTransitionsNarrateANewGap(t *testing.T) {
	lines := hardwareTransitions(nil, []machine.UnclaimedDevice{stick}, nil)
	want := "liken: hardware: unclaimed usb mass-storage device QEMU QEMU USB HARDDRIVE: declare usb_storage or uas in spec.modules"
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

// noisyChannel rings a doorbell continuously, faster than any quiet
// interval: the shape of a node whose containers are churning.
func noisyChannel(t *testing.T) chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 1)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
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
	started := time.Now()
	settle(t.Context(), noisyChannel(t), 50*time.Millisecond, 200*time.Millisecond)
	elapsed := time.Since(started)
	if elapsed < 150*time.Millisecond || elapsed > time.Second {
		t.Errorf("settle returned after %s, want about the 200ms ceiling", elapsed)
	}
}

func TestSettleReturnsAtQuietWhenTheStreamStops(t *testing.T) {
	ch := make(chan struct{}, 1)
	started := time.Now()
	settle(t.Context(), ch, 50*time.Millisecond, 10*time.Second)
	elapsed := time.Since(started)
	if elapsed > time.Second {
		t.Errorf("settle returned after %s, want about the 50ms quiet interval", elapsed)
	}
}

func TestTransitionsAreQuietWhenNothingChanged(t *testing.T) {
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, []machine.UnclaimedDevice{stick}, nil)
	if lines != nil {
		t.Errorf("lines = %q, want none", lines)
	}
}
