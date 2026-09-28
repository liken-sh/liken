package main

// The unit's session sees every line the receiver sends from the
// moment it reads the receiver's state. A line that arrived between
// that read and the moment the unit held the session used to reach no
// session, and a session that missed the line that said the receiver
// is reachable never ran its one-shot. Under load, two reconcile tests
// failed that way about one run in eight.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// staleDriver is a Denon client whose first State read answers a
// receiver not reached yet, and delivers the receiver's real state to
// the unit as a line just before it answers. That is the order a busy
// machine can produce: the line arrives after the session starts to
// read the state and before the unit holds the session.
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

// A unit built by hand around a stale driver, with no status writer,
// so nothing else reads the driver while the test swaps it in.
func TestALineDuringTheSessionStartReachesTheSession(t *testing.T) {
	t.Parallel()
	amp := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	api := startFakeAPI(t)
	unit := &receiverUnit{
		name:       "theater",
		client:     api.client,
		busAddress: brokers.address(),
		readings:   testMetrics(t),
		log:        newReceiverLog(&logBuffer{}, "theater"),
		dirty:      make(chan struct{}, 1),
	}
	unit.setVolume(&ReceiverVolume{Max: 69.5})
	real := denon.NewClient(amp.address(), unit.observe)
	go real.Run(t.Context())
	deadline := time.Now().Add(testTimeout)
	for real.State().Reachable != equipment.ConditionTrue {
		if time.Now().After(deadline) {
			t.Fatal("the client never reached the receiver")
		}
		time.Sleep(time.Millisecond)
	}
	unit.driver = &staleDriver{Client: real, deliver: func() {
		unit.observe(equipment.Event{State: real.State()})
	}}
	t.Cleanup(func() { unit.setSession(context.Background(), nil, "") })

	unit.setSession(t.Context(), playingReceiver(amp.address(), ReceiverVolume{Max: 69.5}).Spec.Session, "")

	amp.waitForCommands(t, "SIGAME")
}
