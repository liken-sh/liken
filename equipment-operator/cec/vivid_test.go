package cec_test

// These tests run against the kernel's CEC core through the vivid
// driver, which emulates a TV with HDMI inputs and sources with HDMI
// outputs, each with a kernel CEC adapter, on one bus. The tests skip
// when this user cannot open a vivid adapter, as in CI. AGENTS.md in
// this directory has the commands that set vivid up.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// vividBus has the vivid adapters this user can open: the TV's adapter
// on the capture side, and each output adapter that vivid connected to
// one of the TV's inputs, which is each one with a physical address.
type vividBus struct {
	tv      *cec.Device
	outputs []*cec.Device
}

func openVivid(t *testing.T) vividBus {
	t.Helper()
	lockVivid(t)
	paths, _ := filepath.Glob("/dev/cec*")
	found := vividBus{}
	for _, path := range paths {
		device, err := cec.Open(path)
		if err != nil {
			continue
		}
		caps, err := device.Caps()
		address, _ := device.PhysicalAddress()
		switch {
		case err != nil || caps.Driver != "vivid":
			device.Close()
		case strings.Contains(caps.Name, "vid-cap"):
			found.tv = device
		case address != cec.InvalidPhysicalAddress:
			found.outputs = append(found.outputs, device)
		default:
			device.Close()
		}
	}
	if found.tv == nil || len(found.outputs) < 2 {
		t.Skip("no vivid TV with two connected outputs is open to this user; see cec/AGENTS.md")
	}
	t.Cleanup(func() {
		for _, device := range append(found.outputs, found.tv) {
			_ = device.Initiate()
			_ = device.Release()
			_ = device.Close()
		}
	})
	return found
}

// follow claims a logical address for device and, until the test ends,
// sends the reply that cec.Answer returns for each message.
func follow(t *testing.T, device *cec.Device, claim cec.Claim, power cec.PowerStatus) cec.LogicalAddress {
	t.Helper()
	mustSucceed(t, device.Follow())
	// An interrupted earlier run leaves its claim in the kernel, and the
	// kernel refuses a new claim on a configured adapter with EBUSY.
	mustSucceed(t, device.Release())
	mustSucceed(t, device.Claim(claim))
	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 1 {
		t.Fatalf("%s claimed addresses %+v, want one logical address", claim.OSDName, held)
	}
	own := held.Logical[0]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, device, func(message cec.Message) {
			if reply, answers := cec.Answer(message, own, power, true); answers {
				_, _ = device.Transmit(reply, 0, 0)
			}
		}, func(cec.Event) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return own
}

func TestVividAScanFindsTheTVAndAnotherSource(t *testing.T) {
	bus := openVivid(t)
	follow(t, bus.tv, cec.Claim{Type: cec.TypeTV, OSDName: "TV"}, cec.PowerStandby)
	follow(t, bus.outputs[1], cec.Claim{OSDName: "player"}, cec.PowerOn)
	own := follow(t, bus.outputs[0], cec.Claim{OSDName: "node-1", Passthrough: true}, cec.PowerOn)
	playerAddress, err := bus.outputs[1].PhysicalAddress()
	mustSucceed(t, err)
	directory := cec.NewDirectory()
	directory.SetOwn(own)

	report, err := cec.Scan(bus.outputs[0], directory, own, nil)

	mustSucceed(t, err)
	peers := directory.Peers()
	t.Logf("scan acked %d addresses: %+v", report.Acked, peers)
	if len(peers) != 2 {
		t.Fatalf("the scan found peers %+v, want the TV and the player", peers)
	}
	tv, player := peers[0], peers[1]
	if tv.Logical != 0 || tv.Physical != 0x0000 || tv.OSDName != "TV" || tv.Type != cec.TypeTV ||
		tv.Version != cec.Version14 || tv.Power != cec.PowerStandby {
		t.Errorf("the TV peer is %+v, want logical 0, physical 0x0000, name TV, type TV, version 1.4, power standby", tv)
	}
	if player.Physical != playerAddress || player.OSDName != "player" || player.Type != cec.TypePlayback || player.Power != cec.PowerOn {
		t.Errorf("the player peer is %+v, want physical %#x, name player, type playback, power on", player, playerAddress)
	}
}

// The kernel refuses to set the physical address of an adapter on a
// video output. cectest refuses it the same way for an adapter without
// CapPhysAddr.
func TestVividAnOutputRefusesAnAddress(t *testing.T) {
	bus := openVivid(t)
	mustSucceed(t, bus.outputs[0].Initiate())

	err := bus.outputs[0].SetPhysicalAddress(0x1300)

	if !errors.Is(err, unix.ENOTTY) {
		t.Errorf("SetPhysicalAddress returned %v, want ENOTTY", err)
	}
}

// A monitor receives a broadcast sent between two other devices. The
// kernel allows monitor-all mode only with CAP_NET_ADMIN, so an
// unprivileged run tests the refusal and a privileged run tests the
// monitor.
func TestVividAMonitorReceivesABroadcastBetweenOtherDevices(t *testing.T) {
	bus := openVivid(t)
	follow(t, bus.tv, cec.Claim{Type: cec.TypeTV, OSDName: "TV"}, cec.PowerStandby)
	player := follow(t, bus.outputs[1], cec.Claim{OSDName: "player"}, cec.PowerOn)
	listener := bus.outputs[0]
	mustSucceed(t, listener.Initiate())
	mustSucceed(t, listener.Release())
	if err := listener.Monitor(true); err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skipf("monitor-all needs CAP_NET_ADMIN: %v", err)
		}
		t.Fatalf("turning on monitor-all mode: %v", err)
	}
	playerAddress, err := bus.outputs[1].PhysicalAddress()
	mustSucceed(t, err)
	directory := cec.NewDirectory()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		_, _ = bus.outputs[1].Transmit(cec.ActiveSource(player, playerAddress), 0, 0)
	}()

	found := func() bool {
		for _, peer := range directory.Peers() {
			if peer.Logical == player && peer.Physical == playerAddress {
				return true
			}
		}
		return false
	}

	_ = cec.Read(ctx, listener, func(message cec.Message) {
		if directory.Observe(message) && found() {
			cancel()
		}
	}, func(cec.Event) {})

	t.Logf("the monitor heard %+v", directory.Peers())
	if !found() {
		t.Errorf("the monitor did not hear the player's Active Source")
	}
}

// The volume keys reach an audio system through the kernel's CEC core
// as a press and a release. The second output acts as the audio system.
func TestVividTheAudioSystemReceivesTheVolumeKeys(t *testing.T) {
	bus := openVivid(t)
	follow(t, bus.tv, cec.Claim{Type: cec.TypeTV, OSDName: "TV"}, cec.PowerOn)
	audio := bus.outputs[1]
	mustSucceed(t, audio.Follow())
	mustSucceed(t, audio.Release())
	mustSucceed(t, audio.Claim(cec.Claim{Type: cec.TypeAudioSystem, OSDName: "AVR"}))
	own := follow(t, bus.outputs[0], cec.Claim{OSDName: "node-1", Passthrough: true}, cec.PowerOn)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var received []string
	go func() {
		_, _ = cec.PressVolumeKey(bus.outputs[0], own, cec.KeyVolumeDown)
	}()

	_ = cec.Read(ctx, audio, func(message cec.Message) {
		if message.From == own {
			received = append(received, fmt.Sprintf("% x", message.Body))
		}
		if len(received) == 2 {
			cancel()
		}
	}, func(cec.Event) {})

	if strings.Join(received, ", ") != "44 42, 45" {
		t.Errorf("the audio system received %v, want a press of Volume Down and a release", received)
	}
}

// lockVivid holds a file lock for the length of one test, because the
// vivid tests of this package and of package main share one emulated
// bus, and go test runs the two packages at the same time.
func lockVivid(t *testing.T) {
	t.Helper()
	// Create the file only when it is missing, because the kernel refuses
	// O_CREAT on another user's file in a sticky directory such as /tmp,
	// and a privileged run of the monitor test shares the file with an
	// ordinary run.
	path := filepath.Join(os.TempDir(), "equipment-operator-vivid.lock")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o644)
	}
	mustSucceed(t, err)
	mustSucceed(t, unix.Flock(int(file.Fd()), unix.LOCK_EX))
	t.Cleanup(func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	})
}
