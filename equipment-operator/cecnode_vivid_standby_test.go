package main

// The power press that turns the room off, against the kernel's own
// CEC core through the vivid driver: the node workload wakes the TV the
// test plays, then sends it Standby once for the press, and the TV
// reports Standby. cec/AGENTS.md gives the commands that set vivid up.

import (
	"os"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestVividAPowerPressPutsTheTVInStandby(t *testing.T) {
	if os.Getenv(vividTVVariable) != "" {
		t.Skipf("%s names another player for the TV, and this test plays the TV itself", vividTVVariable)
	}
	guard := cecWakeGuard
	cecWakeGuard = time.Second
	t.Cleanup(func() { cecWakeGuard = guard })
	tv, output, address := vividPair(t, true)
	heard := playWakingTV(t, tv)
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
	for _, message := range heard() {
		if opcode, _ := message.Opcode(); opcode == cec.OpStandby {
			mustMatch(t, message.String(), cec.PowerCommand(own, false).String())
			standbys++
		}
	}
	mustMatch(t, standbys, 1)
}
