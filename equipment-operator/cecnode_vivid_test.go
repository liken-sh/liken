package main

// The node workload against the kernel's own CEC core, through the
// vivid driver. It skips when no vivid adapter is open to this user,
// which is the case in CI. cec/AGENTS.md gives the commands that set
// vivid up.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// vividPair opens vivid's TV adapter and one output adapter that vivid
// connected to the TV, and answers the output's physical address.
func vividPair(t *testing.T) (*cec.Device, *cec.Device, cec.PhysicalAddress) {
	t.Helper()
	lockVivid(t)
	paths, _ := filepath.Glob("/dev/cec*")
	var tv, output *cec.Device
	address := cec.InvalidPhysicalAddress
	for _, path := range paths {
		device, err := cec.Open(path)
		if err != nil {
			continue
		}
		caps, err := device.Caps()
		physical, _ := device.PhysicalAddress()
		switch {
		case err != nil || caps.Driver != "vivid":
		case strings.Contains(caps.Name, "vid-cap") && tv == nil:
			tv = device
			continue
		case physical != cec.InvalidPhysicalAddress && output == nil:
			output, address = device, physical
			continue
		}
		device.Close()
	}
	if tv == nil || output == nil {
		t.Skip("no vivid TV with a connected output is open to this user; see cec/AGENTS.md")
	}
	t.Cleanup(func() {
		for _, device := range []*cec.Device{tv, output} {
			_ = device.Initiate()
			_ = device.Release()
			_ = device.Close()
		}
	})
	return tv, output, address
}

// playTV makes a vivid adapter the TV, in standby, answering what a
// follower owes, until the test ends.
func playTV(t *testing.T, tv *cec.Device) {
	t.Helper()
	mustSucceed(t, tv.Follow())
	// A run that was interrupted leaves its claim in the kernel, which
	// refuses a new claim on a configured adapter with EBUSY.
	mustSucceed(t, tv.Release())
	mustSucceed(t, tv.Claim(cec.Claim{Type: cec.TypeTV, OSDName: "TV"}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, tv, func(message cec.Message) {
			if reply, answers := cec.Answer(message, cec.AddressTV, cec.PowerStandby); answers {
				_, _ = tv.Transmit(reply, 0, 0)
			}
		}, func(cec.Event) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func TestVividTheNodeWorkloadJoinsAndFindsTheTV(t *testing.T) {
	tv, output, address := vividPair(t)
	playTV(t, tv)
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", output)

	entry := api.waitForEntryWithin(t, "den", "node-1", vividScanTime, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	t.Logf("the entry: %+v", entry)
	mustMatch(t, entry.PhysicalAddress, address.String())
	mustMatch(t, entry.OSDName, "node-1")
	mustMatch(t, entry.Message, "")
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want the TV alone", entry.Devices)
	}
	mustDeepEqual(t, entry.Devices[0], CECDevice{LogicalAddress: 0, PhysicalAddress: "0.0.0.0", Type: "TV", OSDName: "TV", CECVersion: "1.4", Power: "Standby"})
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
