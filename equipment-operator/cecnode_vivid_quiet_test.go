package main

// On the kernel's vivid virtual CEC device, no message reaches the TV
// after the node workload's join scan while nothing changes.

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// playCountingTV plays the TV like playTV, and returns a function that
// counts the messages the TV has received, broadcasts included.
func playCountingTV(t *testing.T, tv *cec.Device) func() int {
	t.Helper()
	claimTV(t, tv)
	var mutex sync.Mutex
	heard := 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cec.Read(ctx, tv, func(message cec.Message) {
			mutex.Lock()
			heard++
			mutex.Unlock()
			if reply, answers := cec.Answer(message, cec.AddressTV, cec.PowerStandby, true); answers {
				_, _ = tv.Transmit(reply, 0, 0)
			}
		}, func(cec.Event) {})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return func() int {
		mutex.Lock()
		defer mutex.Unlock()
		return heard
	}
}

func TestVividAnIdleBusHearsNothingAfterTheJoinScan(t *testing.T) {
	if os.Getenv(vividTVVariable) != "" {
		t.Skipf("%s names another player for the TV, and this test plays the TV itself", vividTVVariable)
	}
	// The vivid driver runs on the real clock, so a short retry interval
	// lets many retries happen during the two seconds of nudges below.
	retry := cecAPIRetry
	cecAPIRetry = 5 * time.Millisecond
	t.Cleanup(func() { cecAPIRetry = retry })
	tv, output, address := vividPair(t, true)
	heard := playCountingTV(t, tv)
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", address.String())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(""))
	startNode(t, api, "node-1", output)
	api.waitForEntryWithin(t, "den", "node-1", vividScanTime, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	scanned := heard()

	for range 20 {
		api.nudge()
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("the TV heard %d messages during the join, and %d after it", scanned, heard()-scanned)
	mustMatch(t, heard(), scanned)
}
