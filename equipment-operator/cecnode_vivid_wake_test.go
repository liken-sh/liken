package main

// A wake on a vivid bus. The test acts as the TV and records what it
// receives. A second vivid output acts as a streaming player that takes
// the input, and the node workload must take it back. cec/VIVID.md has
// the commands that set up vivid.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// playPlayer makes a vivid output other than skip act as a streaming
// player until the test ends, and returns its physical address. The
// player sends Active Source once, half a second after the first Active
// Source from another device.
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

// waitForClaim waits until the kernel has claimed a logical address for
// the adapter, and returns it. vivid runs the bus at real CEC speed, so
// the claim takes time.
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
	// The wake starts as soon as the node workload joins, and a TV with
	// no logical address yet does not acknowledge Image View On.
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
		t.Errorf("the WakeApplied message is %q, want it to say the player claimed the input once", applied.Message)
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
	viewOn := cec.ImageViewOn(own, cec.AddressTV).String()
	claimed := cec.ActiveSource(own, address).String()
	mustDeepEqual(t, seen, []string{
		viewOn,
		viewOn,
		claimed,
		cec.ActiveSource(senderOfActiveSource(t, heard(), player), player).String(),
		viewOn,
		claimed,
	})
}

// senderOfActiveSource returns the logical address of the device that
// sent the TV an Active Source for the physical address.
func senderOfActiveSource(t *testing.T, heard []cec.Message, physical cec.PhysicalAddress) cec.LogicalAddress {
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
