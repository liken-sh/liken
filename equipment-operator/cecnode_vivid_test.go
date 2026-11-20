package main

// Tests of the node workload against the kernel's CEC core, using the
// vivid driver's emulated adapters. They skip when this user cannot
// open a vivid adapter, as in CI. cec/VIVID.md has the commands that
// set up vivid.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// vividTVVariable selects what acts as the TV on vivid's capture
// adapter. When it is unset, the test acts as the TV. When it is
// cec-follower, the cec-follower program from v4l-utils acts as the TV,
// and the test leaves the capture adapter alone. cec/VIVID.md has the
// cec-follower command.
const vividTVVariable = "CEC_VIVID_TV"

// vividPair opens vivid's TV adapter and an output adapter connected to
// it, and returns both with the output's physical address. With playsTV
// false, vividPair closes the TV handle at once, so cleanup does not
// release the logical address that cec-follower claimed.
func vividPair(t *testing.T, playsTV bool) (*cec.Device, *cec.Device, cec.PhysicalAddress) {
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
	owned := []*cec.Device{tv, output}
	if !playsTV {
		_ = tv.Close()
		owned = owned[1:]
	}
	t.Cleanup(func() {
		for _, device := range owned {
			_ = device.Initiate()
			_ = device.Release()
			_ = device.Close()
		}
	})
	return tv, output, address
}

// claimTV makes a vivid adapter a follower with TV logical address 0.
// To claim a TV address the kernel polls address 0, and it takes 14
// instead when the poll gets neither an ACK nor a NACK. vivid sometimes
// does that even when no adapter has address 0, so claimTV tries the
// claim up to three times.
func claimTV(t *testing.T, tv *cec.Device) {
	t.Helper()
	mustSucceed(t, tv.Follow())
	for range 3 {
		// A claim from an interrupted earlier run stays in the kernel,
		// which refuses a new claim on a configured adapter with EBUSY.
		mustSucceed(t, tv.Release())
		mustSucceed(t, tv.Claim(cec.Claim{Type: cec.TypeTV, OSDName: "TV"}))
		held, err := tv.Addresses()
		mustSucceed(t, err)
		if slices.Equal(held.Logical, []cec.LogicalAddress{cec.AddressTV}) {
			return
		}
		t.Logf("the TV claimed %v and not 0; claiming again", held.Logical)
	}
	t.Fatal("the TV did not claim logical address 0")
}

// playTV makes a vivid adapter act as a TV in standby until the test
// ends. It replies to the messages that a CEC follower must reply to.
func playTV(t *testing.T, tv *cec.Device) {
	t.Helper()
	claimTV(t, tv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, tv, func(message cec.Message) {
			if reply, answers := cec.Answer(message, cec.AddressTV, cec.PowerStandby, true); answers {
				_, _ = tv.Transmit(reply, 0, 0)
			}
		}, func(cec.Event) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func TestVividTheNodeWorkloadJoinsAndFindsTheTV(t *testing.T) {
	if os.Getenv(vividTVVariable) != "" {
		t.Skipf("%s names another player for the TV, and this test plays the TV itself", vividTVVariable)
	}
	tv, output, address := vividPair(t, true)
	playTV(t, tv)
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", output)

	entry := api.waitForEntryWithin(t, "den", "node-1", vividScanTime, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	t.Logf("the entry: %+v", entry)
	mustMatch(t, entry.PhysicalAddress, address.String())
	mustMatch(t, entry.OSDName, "liken")
	mustMatch(t, entry.Message, "")
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want the TV alone", entry.Devices)
	}
	mustDeepEqual(t, entry.Devices[0], CECDevice{LogicalAddress: 0, PhysicalAddress: "0.0.0.0", Type: "TV", OSDName: "TV", CECVersion: "1.4", Power: "Standby"})
}

// lockVivid takes an exclusive lock on a file until the test ends. The
// vivid tests of this package and of the cec package share one emulated
// bus, and go test runs the packages at the same time.
func lockVivid(t *testing.T) {
	t.Helper()
	// Create the file only when it does not exist. The kernel refuses
	// O_CREAT on a file that another user owns in a sticky directory such
	// as /tmp, and a privileged run of the cec package's monitor test can
	// leave the file owned by root.
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

// playWakingTV makes a vivid adapter act as a TV in standby that obeys
// power commands until the test ends. Image View On sets its power to
// ToOn and, a second later, to On, the way a real TV reports a wake.
// Standby moves it through ToStandby to Standby the same way. The
// returned function returns every message the TV received except polls,
// in order.
func playWakingTV(t *testing.T, tv *cec.Device) func() []cec.Message {
	t.Helper()
	var heardMutex sync.Mutex
	var heard []cec.Message
	claimTV(t, tv)
	var mutex sync.Mutex
	power, settles := cec.PowerStandby, time.Time{}
	current := func() cec.PowerStatus {
		mutex.Lock()
		defer mutex.Unlock()
		if !settles.IsZero() && time.Now().After(settles) {
			power = map[cec.PowerStatus]cec.PowerStatus{cec.PowerToOn: cec.PowerOn, cec.PowerToStandby: cec.PowerStandby}[power]
			settles = time.Time{}
		}
		return power
	}
	move := func(through, to cec.PowerStatus) {
		if current() == to {
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		power, settles = through, time.Now().Add(time.Second)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, tv, func(message cec.Message) {
			opcode, _ := message.Opcode()
			if !message.IsPoll() {
				heardMutex.Lock()
				heard = append(heard, message)
				heardMutex.Unlock()
			}
			switch {
			case message.IsPoll():
			case opcode == cec.OpImageViewOn && message.To == cec.AddressTV:
				move(cec.PowerToOn, cec.PowerOn)
				return
			case opcode == cec.OpStandby:
				move(cec.PowerToStandby, cec.PowerStandby)
				return
			}
			if reply, answers := cec.Answer(message, cec.AddressTV, current(), true); answers {
				_, _ = tv.Transmit(reply, 0, 0)
			}
		}, func(cec.Event) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return func() []cec.Message {
		heardMutex.Lock()
		defer heardMutex.Unlock()
		return slices.Clone(heard)
	}
}

// vividPowerTime is the longest a test waits for one application of
// spec.power on vivid. The node workload joins and scans at the speed
// of a real CEC bus, and a TV that cec-follower plays is slow to wake.
const vividPowerTime = 60 * time.Second

// The node workload puts the vivid TV in standby, then wakes it, and
// reads the TV's power after each command. The Deployment's pass copies
// that power into the Television. The node workload sends no command to
// a TV that already reports the requested power, so the test requests
// Standby first, and the request for On then needs a command whatever
// power the TV started in.
func TestVividTheNodeWorkloadWakesTheTV(t *testing.T) {
	external := os.Getenv(vividTVVariable) == "cec-follower"
	tv, output, address := vividPair(t, !external)
	if !external {
		playWakingTV(t, tv)
	}
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(TelevisionStandby))
	startNode(t, api, "node-1", output)
	api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.PowerGeneration == 1
	})

	api.putTelevision(lounge(TelevisionOn))

	television := api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.PowerGeneration == 2
	})
	applied := conditionOf(television.Status.Conditions, conditionPowerApplied)
	t.Logf("PowerApplied: %s %s: %s", applied.Status, applied.Reason, applied.Message)
	mustMatch(t, applied.Status, ConditionTrue)
	if !strings.Contains(applied.Message, "sent Image View On") {
		t.Errorf("the PowerApplied message is %q, want it to contain \"sent Image View On\"", applied.Message)
	}
	// The node workload reports the TV's power in its CECBus entry, and
	// it writes the entry after it confirms the new power.
	api.waitForEntryWithin(t, "den", "node-1", vividPowerTime, func(entry CECAdapterStatus) bool {
		return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == "On"
	})
	passes(t, api, 1)
	television, _ = api.television("lounge")
	t.Logf("status.power %s, status.cec %+v", television.Status.Power, television.Status.CEC)
	mustMatch(t, television.Status.Power, "On")
}
