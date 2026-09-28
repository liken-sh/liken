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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// vividTVVariable names what plays vivid's TV. Unset, a test plays the
// TV itself on vivid's capture adapter. With the value cec-follower,
// cec-follower from v4l-utils plays it, and the test leaves the
// capture adapter alone; cec/AGENTS.md gives the command.
const vividTVVariable = "CEC_VIVID_TV"

// vividPair opens vivid's TV adapter and one output adapter that vivid
// connected to the TV, and answers the output's physical address. The
// TV's handle is for a test that plays the TV. A test that leaves the
// TV to cec-follower passes playsTV false, and the handle closes with
// no change to the claim cec-follower holds.
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

// claimTV makes a vivid adapter a follower that holds the TV's logical
// address 0. The kernel claims a TV's address by polling 0, and it
// takes 14 instead when the poll ends in neither an acknowledgement
// nor a NACK. On vivid that happens now and then with no adapter
// holding 0, and a TV at 14 is not the TV any test expects, so the
// claim is made again, a few times at most.
func claimTV(t *testing.T, tv *cec.Device) {
	t.Helper()
	mustSucceed(t, tv.Follow())
	for range 3 {
		// A run that was interrupted leaves its claim in the kernel, which
		// refuses a new claim on a configured adapter with EBUSY.
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

// playTV makes a vivid adapter the TV, in standby, answering what a
// follower owes, until the test ends.
func playTV(t *testing.T, tv *cec.Device) {
	t.Helper()
	claimTV(t, tv)
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
	mustMatch(t, entry.OSDName, "den")
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

// playWakingTV makes a vivid adapter a TV in standby that obeys the
// power commands, until the test ends. Image View On moves it to ToOn,
// and a second later to On, because a TV takes time to wake and
// reports the transition while it does. Standby moves it the same way
// to Standby. It answers a read of every message the TV heard, polls
// aside, in order.
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
			if reply, answers := cec.Answer(message, cec.AddressTV, current()); answers {
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

// vividPowerTime bounds one application of spec.power on vivid: the
// node workload joins and scans at the speed of a real CEC wire, and a
// TV that cec-follower plays takes about 9 seconds to wake.
const vividPowerTime = 60 * time.Second

// The node workload puts the kernel's TV in standby and wakes it again,
// and each time reads the power back from the TV. The Deployment's pass
// then copies the TV's power into the Television. A TV that already
// reports the state gets no command, so the test asks for Standby
// first, whatever state the TV is in when the test starts.
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
		t.Errorf("the TV was on before the command: %s", applied.Message)
	}
	// The node workload's entry carries the TV's power to the
	// Deployment, and the entry write follows the confirmation.
	api.waitForEntryWithin(t, "den", "node-1", vividPowerTime, func(entry CECAdapterStatus) bool {
		return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == "On"
	})
	passes(t, api, 1)
	television, _ = api.television("lounge")
	t.Logf("status.power %s, status.cec %+v", television.Status.Power, television.Status.CEC)
	mustMatch(t, television.Status.Power, "On")
}
