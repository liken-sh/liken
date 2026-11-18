package main

// setSession stores a new session on the unit before the session reads
// the receiver's state, so every event that arrives after the read
// reaches the session. In the other order, an event between the read and
// the store would reach no session, and a session that missed the event
// that reports the receiver reachable would never run its one-shot.

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// staleDriver is a Denon client whose first State call sends the
// receiver's real state to the unit as an event and then returns an
// empty state, as if the receiver were not reached yet. So the event
// arrives during the session's first read, which a busy machine can
// also cause.
type staleDriver struct {
	*denon.Client
	once    sync.Once
	deliver func()
}

func (d *staleDriver) State() equipment.State {
	stale := false
	d.once.Do(func() {
		d.deliver()
		stale = true
	})
	if stale {
		return equipment.State{}
	}
	return d.Client.State()
}

// The unit is built by hand with a stale driver and no status writer,
// so nothing else reads the driver while the test swaps it in.
func TestALineDuringTheSessionStartReachesTheSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeDenon(t)
		api := startFakeAPI(t)
		unit := &receiverUnit{
			name:     "theater",
			client:   api.client,
			dial:     testNetwork.dial,
			readings: testMetrics(t),
			log:      newReceiverLog(&logBuffer{}, "theater"),
			dirty:    make(chan struct{}, 1),
		}
		real := denon.NewClient(amp.address(), unit.observe)
		real.Dial = testNetwork.dial
		go real.Run(t.Context())
		deadline := time.Now().Add(testTimeout)
		for real.State().Reachable != equipment.ConditionTrue {
			if time.Now().After(deadline) {
				t.Fatal("the Denon client is still not reachable after the test timeout")
			}
			time.Sleep(time.Millisecond)
		}
		unit.driver = &staleDriver{Client: real, deliver: func() {
			unit.observe(equipment.Event{State: real.State()})
		}}
		t.Cleanup(func() { unit.setSession(context.Background(), nil, "") })

		unit.setSession(t.Context(), playingReceiver(amp.address(), ReceiverVolume{Max: 69.5}).Spec.Session, "")

		amp.waitForCommands(t, "SIGAME")
	})
}
