package main

// An operator that stops at shutdown hands each session to the next
// operator. The owner mark stays on the broker through the restart, so
// the playback pods leave the level alone in the gap, and the broker
// drops the will because the session closes with DISCONNECT. A crash
// sends no DISCONNECT, so the will still clears the mark then.

import (
	"testing"
	"time"
)

func TestAShutdownKeepsTheOwnerMark(t *testing.T) {
	t.Parallel()
	api := startFakeAPI(t)
	amp := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }
	api.setReceivers(idleReceiver(amp.address(), ReceiverVolume{Max: 69.5}))
	mustSucceed(t, operator.pass(t.Context()))
	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))

	operator.stopAll()

	broker.waitForDisconnect(t)
	for _, published := range broker.drained() {
		if published.topic == ownerTopic(testVolumeTopic) {
			t.Errorf("the shutdown published %q on the owner topic", published.payload)
		}
	}
}

// drained answers the publishes the broker read and no test took yet.
// The broker reads the client's packets in order, so after a
// DISCONNECT every publish before it is here.
func (b *fakeBroker) drained() []brokerPublish {
	var held []brokerPublish
	for {
		select {
		case published := <-b.pubs:
			held = append(held, published)
		default:
			return held
		}
	}
}
