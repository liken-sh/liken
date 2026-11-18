package main

// A clear is an empty retained publish. When the bus drops a clear
// because its write queue is full, the old retained message stays on
// the broker for every later subscriber. So the operator removes a
// topic, a run, or a Play from its records only after the bus queued
// the clear, and a later pass publishes a dropped clear again.

import (
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
)

// fillQueue fills the bus's write queue, so the next publish is
// dropped. The fake broker stops reading once its pubs channel is full,
// because nothing reads that channel yet. The first round fills the
// channel and the queue, and the second round refills the queue slots
// that the broker's reads freed.
func fillQueue(bus *Bus) {
	for range 2 {
		for index := range 2 * busQueueDepth {
			bus.Publish(fmt.Sprintf("filler/%d", index), []byte("x"), false)
		}
		synctest.Wait()
	}
}

// drainBroker reads every publish that the broker receives from now on.
// It returns a map from topic to whether the latest publish on that
// topic was a clear. Read the map only after synctest.Wait.
func drainBroker(t *testing.T, broker *fakeBroker) map[string]bool {
	t.Helper()
	cleared := map[string]bool{}
	go func() {
		for {
			select {
			case published := <-broker.pubs:
				cleared[published.topic] = published.retained && len(published.payload) == 0
			case <-t.Context().Done():
				return
			}
		}
	}()
	synctest.Wait()
	return cleared
}

func TestAPlayerStatusClearDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		statuses := &publishedStatuses{}
		topic := playerStatusTopic(defaultTopicBase, "house", "gone")
		publishIdle(t, statuses, bus, []string{topic})
		fillQueue(bus)
		statuses.clear(bus, map[string]bool{})

		cleared := drainBroker(t, brokers[0])
		statuses.clear(bus, map[string]bool{})
		synctest.Wait()

		mustMatch(t, cleared[topic], true)
	})
}

func TestAKeyTableClearDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		media := &operator{bus: bus, topicBase: defaultTopicBase, keysPublished: map[string]string{}}
		topic := remoteKeysTopic(defaultTopicBase, "house", "gone")
		media.keysPublished[topic] = "[]"
		fillQueue(bus)
		media.clearGoneKeys(map[string]bool{})

		cleared := drainBroker(t, brokers[0])
		media.clearGoneKeys(map[string]bool{})
		synctest.Wait()

		mustMatch(t, cleared[topic], true)
	})
}

func TestASweepDroppedOnAFullQueueClearsTheRunOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		media := &operator{topicBase: defaultTopicBase, bus: bus, reports: newReports(make(chan struct{}, 1))}
		media.reports.availability("den", "old-film", false, "")
		fillQueue(bus)
		media.reclaimPlays(map[string]bool{})

		cleared := drainBroker(t, brokers[0])
		media.reclaimPlays(map[string]bool{})
		synctest.Wait()

		mustMatch(t, cleared[playStatusTopic(defaultTopicBase, "den", "old-film")], true)
		mustMatch(t, cleared[playAvailabilityTopic(defaultTopicBase, "den", "old-film")], true)
	})
}

func TestAReleaseWhoseClearWasDroppedKeepsThePlayUntilTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		play := playOnPlayer("movie", "theater", "2026-09-08T10:00:00Z")
		play.Metadata.DeletionTimestamp = "2026-09-08T10:30:00Z"
		play.Metadata.Finalizers = []string{playFinalizer}
		cluster.plays["movie"] = play
		media, broker := busOperator(t, cluster)
		fillQueue(media.bus)

		media.pass()

		kept, exists := cluster.plays["movie"]
		if !exists || !reflect.DeepEqual(kept.Metadata.Finalizers, []string{playFinalizer}) {
			t.Fatal("the Play lost its finalizer after its clear was dropped, want it kept until the next pass")
		}
		cleared := drainBroker(t, broker)
		media.pass()
		synctest.Wait()

		mustMatch(t, cleared[playStatusTopic(defaultTopicBase, "house", "movie")], true)
		mustMatch(t, cleared[playAvailabilityTopic(defaultTopicBase, "house", "movie")], true)
		if _, exists := cluster.plays["movie"]; exists {
			t.Error("the Play still exists after the next pass queued its clear, want it deleted")
		}
	})
}

func TestAPanelClearDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := screenCluster()
		media, broker := busOperator(t, cluster)
		sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
		player := *cluster.players["theater"]
		player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
		fillQueue(media.bus)
		media.reconcilePlayers([]Player{player}, nil, "", nil)

		cleared := drainBroker(t, broker)
		media.reconcilePlayers([]Player{player}, nil, "", nil)
		synctest.Wait()

		mustMatch(t, cleared[playerPanelTopic(defaultTopicBase, "house", "theater")], true)
	})
}
