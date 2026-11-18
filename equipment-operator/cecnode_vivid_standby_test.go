package main

// This test runs a standby press against the kernel's CEC core through
// the vivid driver, with one vivid adapter acting as the TV. The node
// workload wakes the TV, and for the press it must send exactly one
// Standby. cec/AGENTS.md has the commands that set up vivid.

import (
	"os"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

func TestVividAPowerPressPutsTheTVInStandby(t *testing.T) {
	if os.Getenv(vividTVVariable) != "" {
		t.Skipf("%s is set, so another program plays the TV, and this test plays the TV itself", vividTVVariable)
	}
	guard := cecWakeGuard
	cecWakeGuard = time.Second
	t.Cleanup(func() { cecWakeGuard = guard })
	tv, output, address := vividPair(t, true)
	received := playWakingTV(t, tv)
	waitForClaim(t, tv)
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(""))
	startNode(t, api, "node-1", output)
	api.waitForEntryWithin(t, "den", "node-1", vividScanTime, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	woke := wokeNow()
	api.putTelevision(waking(woke))
	api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.WokeAt == woke.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason != reasonWaking
	})
	off := pressedOff(woke)

	api.putTelevision(waking(off))

	television := api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.StandbyAt == off.StandbyAt && conditionOf(television.Status.Conditions, conditionStandbyApplied).Reason != reasonEnteringStandby
	})
	applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
	t.Logf("StandbyApplied: %s %s: %s", applied.Status, applied.Reason, applied.Message)
	mustMatch(t, applied.Status, ConditionTrue)
	entry, _ := api.entry("den", "node-1")
	own := cec.LogicalAddress(*entry.LogicalAddress)
	standbys := 0
	for _, message := range received() {
		if opcode, _ := message.Opcode(); opcode == cec.OpStandby {
			mustMatch(t, message.String(), cec.PowerCommand(own, false).String())
			standbys++
		}
	}
	mustMatch(t, standbys, 1)
}
