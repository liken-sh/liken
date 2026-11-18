package main

// After a wake, the test acts as the TV on a vivid bus and sends Request
// Active Source, and the node workload must reply with Active Source.
// cec/AGENTS.md has the commands that set up vivid.

import (
	"os"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

func TestVividTheAdapterRepliesToRequestActiveSource(t *testing.T) {
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
	session := wokeNow()
	api.putTelevision(waking(session))
	api.waitForTelevisionWithin(t, "lounge", vividPowerTime, func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason != reasonWaking
	})
	entry, _ := api.entry("den", "node-1")
	claim := cec.ActiveSource(cec.LogicalAddress(*entry.LogicalAddress), address).String()
	claims := func() int {
		count := 0
		for _, message := range heard() {
			if message.String() == claim {
				count++
			}
		}
		return count
	}
	before := claims()

	_, err := tv.Transmit(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpRequestActiveSource), 0, 0)
	mustSucceed(t, err)

	api.waitUntil(t, "the answer", func() bool { return claims() == before+1 })
}
