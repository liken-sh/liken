package cec_test

// These tests run against the kernel's own CEC core through the vivid
// driver, which emulates a TV with HDMI inputs and sources on HDMI
// outputs, each with a real kernel CEC adapter on one shared bus. They
// skip when no vivid adapter is open to this user, which is the case
// in CI. AGENTS.md in this directory gives the commands that set vivid
// up.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// vividBus is the vivid adapters this user can open: the TV's adapter
// on the capture side, and every output adapter that has a physical
// address, which means vivid connected it to one of the TV's inputs.
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

// follow claims an address for a device and answers what a follower
// owes, until the test ends.
func follow(t *testing.T, device *cec.Device, claim cec.Claim, power cec.PowerStatus) cec.LogicalAddress {
	t.Helper()
	mustSucceed(t, device.Follow())
	// A run that was interrupted leaves its claim in the kernel, which
	// refuses a new claim on a configured adapter with EBUSY.
	mustSucceed(t, device.Release())
	mustSucceed(t, device.Claim(claim))
	held, err := device.Addresses()
	mustSucceed(t, err)
	if len(held.Logical) != 1 {
		t.Fatalf("%s claimed %+v", claim.OSDName, held)
	}
	own := held.Logical[0]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, device, func(message cec.Message) {
			if reply, answers := cec.Answer(message, own, power); answers {
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

	report, err := cec.Scan(bus.outputs[0], directory, own)

	mustSucceed(t, err)
	peers := directory.Peers()
	t.Logf("scan acked %d addresses: %+v", report.Acked, peers)
	if len(peers) != 2 {
		t.Fatalf("peers = %+v, want the TV and the player", peers)
	}
	tv, player := peers[0], peers[1]
	if tv.Logical != 0 || tv.Physical != 0x0000 || tv.OSDName != "TV" || tv.Type != cec.TypeTV ||
		tv.Version != cec.Version14 || tv.Power != cec.PowerStandby {
		t.Errorf("tv = %+v", tv)
	}
	if player.Physical != playerAddress || player.OSDName != "player" || player.Type != cec.TypePlayback || player.Power != cec.PowerOn {
		t.Errorf("player = %+v", player)
	}
}

// The kernel keeps the address of an adapter on a video port, which is
// the behavior cectest copies for an adapter with no CapPhysAddr.
func TestVividAnOutputRefusesAnAddress(t *testing.T) {
	bus := openVivid(t)
	mustSucceed(t, bus.outputs[0].Initiate())

	err := bus.outputs[0].SetPhysicalAddress(0x1300)

	if !errors.Is(err, unix.ENOTTY) {
		t.Errorf("got %v, want ENOTTY", err)
	}
}

// A monitor hears a broadcast between two other devices. The kernel
// allows monitor-all only with CAP_NET_ADMIN, so an unprivileged run
// proves the refusal and a privileged run proves the monitor.
func TestVividAMonitorHearsTheBus(t *testing.T) {
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
		t.Fatal(err)
	}
	playerAddress, err := bus.outputs[1].PhysicalAddress()
	mustSucceed(t, err)
	directory := cec.NewDirectory()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		_, _ = bus.outputs[1].Transmit(cec.ActiveSource(player, playerAddress), 0, 0)
	}()

	heard := func() bool {
		for _, peer := range directory.Peers() {
			if peer.Logical == player && peer.Physical == playerAddress {
				return true
			}
		}
		return false
	}

	_ = cec.Read(ctx, listener, func(message cec.Message) {
		if directory.Observe(message) && heard() {
			cancel()
		}
	}, func(cec.Event) {})

	t.Logf("the monitor heard %+v", directory.Peers())
	if !heard() {
		t.Errorf("the monitor did not hear the player's Active Source")
	}
}

// lockVivid holds a lock file for the length of one test. The vivid
// tests of this package and of the other package that has them share
// one emulated bus, and go test runs the two packages at once, so a
// test waits here until the other package's test releases the bus.
func lockVivid(t *testing.T) {
	t.Helper()
	// The lock opens read-only, and the file is created only when it is
	// missing, because the kernel refuses O_CREAT on another user's file
	// in a sticky directory such as /tmp, and a privileged run of the
	// monitor test shares the file with an ordinary one.
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
