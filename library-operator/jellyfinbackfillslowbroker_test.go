package main

// What these tests prove: a backfill counts a play only when the bus took
// it, and fails when it cannot say that every play reached the broker. A
// play is not retained and the run publishes it once, so a play that the
// bus dropped would be missing from the progress store while the Job
// reported success.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// backfillOfManyPlays is a backfill of one user with twice as many items as
// the bus's write queue holds, on a bus that dials a broker that retains
// messages.
func backfillOfManyPlays(t *testing.T) (*jellyfinBackfill, *retainBroker) {
	t.Helper()
	items := make([]jellyfinItem, 0, 2*busQueueDepth)
	for index := range cap(items) {
		items = append(items, jellyfinItem{ID: fmt.Sprintf("item-%d", index), Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": fmt.Sprintf("%d", 5000+index)}})
	}
	backfill, _, _ := standBackfill(t, &fakeBackfillJellyfin{
		users:     []jellyfinUser{{Name: "Person-A", ID: "user-a"}},
		resumable: map[string][]jellyfinItem{"user-a": items},
	})
	broker := newRetainBroker(t)
	ready, once := make(chan struct{}), sync.Once{}
	backfill.ready = ready
	backfill.bus = newBus("pipe", "jellyfin-backfill-house", nil,
		func(*Bus) { once.Do(func() { close(ready) }) }, nil)
	backfill.bus.dial = broker.dial
	backfill.publish = backfill.bus.PublishWait
	return backfill, broker
}

// outsidePlaysOn counts the outside plays that the broker received.
func outsidePlaysOn(broker *retainBroker) int {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	received := 0
	for _, topic := range broker.published {
		if strings.Contains(topic, "/outside") {
			received++
		}
	}
	return received
}

func TestABackfillPublishesEveryPlayWhileTheBrokerFallsBehind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backfill, broker := backfillOfManyPlays(t)
		release := broker.holdReads()
		go func() {
			time.Sleep(500 * time.Millisecond)
			release()
		}()

		counts, err := backfill.runOnBus(t.Context())

		if err != nil {
			t.Fatal(err)
		}
		if received := outsidePlaysOn(broker); received != counts.published || received != 2*busQueueDepth {
			t.Errorf("the broker received %d plays, and the run counted %d published of %d",
				received, counts.published, 2*busQueueDepth)
		}
	})
}

func TestABackfillFailsWhenTheBrokerNeverReadsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backfill, broker := backfillOfManyPlays(t)
		t.Cleanup(broker.holdReads())

		counts, err := backfill.runOnBus(t.Context())

		if err == nil || counts.published >= 2*busQueueDepth {
			t.Errorf("the run counted %d plays published with the error %v, while the broker read none",
				counts.published, err)
		}
	})
}

func TestABackfillFailsWhenItsConnectionEndedDuringTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backfill, broker := backfillOfManyPlays(t)
		go func() {
			time.Sleep(10 * time.Millisecond)
			broker.restart()
		}()

		_, err := backfill.runOnBus(t.Context())

		if err == nil {
			t.Error("the run reported no error after the broker closed its connection")
		}
	})
}
