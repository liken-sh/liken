package main

// The wake against the kernel's own CEC core, through the vivid
// driver: the node workload wakes the TV, makes its Display the active
// source, and takes the input back from a second vivid output that
// plays a streaming player. The TV the test plays records what it
// heard, which is what the bus reports at the TV. cec/AGENTS.md gives
// the commands that set vivid up.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// playPlayer opens a vivid output other than the one at skip, and
// makes it a streaming player that claims Active Source once, after
// another device's claim, until the test ends. It answers the player's
// physical address.
func playPlayer(t *testing.T, skip cec.PhysicalAddress) cec.PhysicalAddress {
	t.Helper()
	paths, _ := filepath.Glob("/dev/cec*")
	for _, path := range paths {
		device, err := cec.Open(path)
		if err != nil {
			continue
		}
		caps, err := device.Caps()
		physical, _ := device.PhysicalAddress()
		if err != nil || caps.Driver != "vivid" || strings.Contains(caps.Name, "vid-cap") || physical == cec.InvalidPhysicalAddress || physical == skip {
			device.Close()
			continue
		}
		mustSucceed(t, device.Follow())
		mustSucceed(t, device.Release())
		mustSucceed(t, device.Claim(cec.Claim{OSDName: "Player"}))
		held := waitForClaim(t, device)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		claims := 1
		go func() {
			defer close(done)
			_ = cec.Read(ctx, device, func(message cec.Message) {
				opcode, _ := message.Opcode()
				if opcode != cec.OpActiveSource || message.From == held || claims == 0 {
					return
				}
				claims--
				time.AfterFunc(500*time.Millisecond, func() { _, _ = device.Transmit(cec.ActiveSource(held, physical), 0, 0) })
			}, func(cec.Event) {})
		}()
		t.Cleanup(func() {
			cancel()
			<-done
			_ = device.Initiate()
			_ = device.Release()
			_ = device.Close()
		})
		return physical
	}
	t.Skip("no second vivid output is connected to the TV; see cec/AGENTS.md")
	return cec.InvalidPhysicalAddress
}

// waitForClaim waits for the kernel to finish an adapter's claim, which
// polls the wire at the speed of a real CEC bus, and answers the
// address.
func waitForClaim(t *testing.T, device *cec.Device) cec.LogicalAddress {
	t.Helper()
	deadline := time.Now().Add(vividScanTime)
	for time.Now().Before(deadline) {
		held, err := device.Addresses()
		mustSucceed(t, err)
		if len(held.Logical) > 0 {
			return held.Logical[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the kernel claimed no logical address for the adapter")
	return 0
}

func TestVividTheWakeShowsTheDisplay(t *testing.T) {
	if os.Getenv(vividTVVariable) != "" {
		t.Skipf("%s names another player for the TV, and this test plays the TV itself", vividTVVariable)
	}
	guard, settle := cecWakeGuard, cecWakeSettle
	cecWakeGuard, cecWakeSettle = 5*time.Second, 500*time.Millisecond
	t.Cleanup(func() { cecWakeGuard, cecWakeSettle = guard, settle })
	tv, output, address := vividPair(t, true)
	heard := playWakingTV(t, tv)
	// The wake starts once the node workload joins, which can be before
	// the kernel finishes the TV's claim, and a TV with no address NACKs
	// Image View On.
	waitForClaim(t, tv)
	player := playPlayer(t, address)
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(""))
	startNode(t, api, "node-1", output)
	api.waitForEntryWithin(t, "den", "node-1", vividScanTime, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	session := wokeNow()

	api.putTelevision(waking(session))

	television := api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason != reasonWaking
	})
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	t.Logf("WakeApplied: %s %s: %s", applied.Status, applied.Reason, applied.Message)
	mustMatch(t, applied.Status, ConditionTrue)
	if !strings.Contains(applied.Message, "claimed the input once") {
		t.Errorf("the player's claim is not in the message: %s", applied.Message)
	}
	var seen []string
	for _, message := range heard() {
		if opcode, _ := message.Opcode(); opcode == cec.OpImageViewOn || opcode == cec.OpActiveSource {
			seen = append(seen, message.String())
		}
	}
	t.Logf("the TV heard %q", seen)
	entry, _ := api.entry("den", "node-1")
	own := cec.LogicalAddress(*entry.LogicalAddress)
	claimed := cec.ActiveSource(own, address).String()
	mustDeepEqual(t, seen, []string{
		cec.ImageViewOn(own, cec.AddressTV).String(),
		claimed,
		cec.ActiveSource(seenFrom(t, heard(), player), player).String(),
		claimed,
	})
}

// seenFrom answers the logical address the TV heard claim a physical
// address.
func seenFrom(t *testing.T, heard []cec.Message, physical cec.PhysicalAddress) cec.LogicalAddress {
	t.Helper()
	for _, message := range heard {
		operands := message.Operands()
		if opcode, _ := message.Opcode(); opcode == cec.OpActiveSource && len(operands) >= 2 && cec.PhysicalAddress(operands[0])<<8|cec.PhysicalAddress(operands[1]) == physical {
			return message.From
		}
	}
	t.Fatalf("the TV heard no Active Source for %s", physical)
	return 0
}
