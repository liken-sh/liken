package main

// What these tests prove: a clear that the bus dropped because its write
// queue was full goes out again on a later pass. A clear is an empty
// retained publish, and a dropped one leaves the old message on the broker
// for every later subscriber, so the operator forgets a topic, a run, or a
// Play only once the bus queued its clear.

import (
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
)

// fillQueue publishes more frames than the bus's write queue holds while the
// broker takes none of them, so the next publish is dropped. The first
// round runs while the broker still reads the frames its buffer holds, and
// the second fills the slots that reading freed.
func fillQueue(bus *Bus) {
	for range 2 {
		for index := range 2 * busQueueDepth {
			bus.Publish(fmt.Sprintf("filler/%d", index), []byte("x"), false)
		}
		synctest.Wait()
	}
}

// drainBroker takes every publish the broker reads from now on, and answers
// a read of each topic that received a retained empty payload. A caller
// reads it after synctest.Wait.
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

		held, standing := cluster.plays["movie"]
		if !standing || !reflect.DeepEqual(held.Metadata.Finalizers, []string{playFinalizer}) {
			t.Fatalf("a release whose clear was dropped took the finalizer off")
		}
		cleared := drainBroker(t, broker)
		media.pass()
		synctest.Wait()

		mustMatch(t, cleared[playStatusTopic(defaultTopicBase, "house", "movie")], true)
		mustMatch(t, cleared[playAvailabilityTopic(defaultTopicBase, "house", "movie")], true)
		if _, standing := cluster.plays["movie"]; standing {
			t.Error("the Play is still held after its clear was queued")
		}
	})
}

func TestAPanelClearDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := screenCluster()
		media, broker := busOperator(t, cluster)
		statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
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
