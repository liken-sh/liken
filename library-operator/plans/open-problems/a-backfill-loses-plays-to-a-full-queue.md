# A Jellyfin backfill loses plays to a full queue

A Jellyfin backfill can report that it published every play while the
progress store never receives some of them. The cause is a publish that
the backfill's bus client drops and that nothing sends again.

## What happens

The backfill Job (`jellyfinbackfill.go`) reads each user's resumable and
played items from Jellyfin, and publishes each one as an outside play on
`plays/{namespace}/{name}/outside`. An outside play is not retained,
the way the webhook path publishes it, so the progress role records it
only if the role reads it as it arrives. The backfill waits
`jellyfinBackfillPace` between two publishes so that the client's writer
keeps its queue short.

`Bus.Publish` drops a frame when its write queue of `busQueueDepth`
frames is full. While the broker reads slower than the backfill
publishes, the queue fills, and each later play is dropped. `carry`
ignores the answer of the publish and counts the play as published. The
run ends with no error, so the Job succeeds and the operator does not
run it again. The broker holds no copy of a dropped play, and nothing
publishes it again.

The jellyfin role's reconcile (`jellyfinreconcile.go`) carries each item
through the same code when the progress role reports online, so it can
drop plays the same way.

## What a user sees

After a backfill that reported success, some plays are missing from the
progress store. The Job's log line counts every play as published, for
example "backfilled 1 users: 128 plays published, 0 items skipped". The
titles behind the
missing plays show no resume point and no watched mark on the screens,
although Jellyfin holds both.

## How to reproduce it

1. Give the backfill one user with more items than the bus's write queue
   holds.
2. Connect it to a broker that stops reading for half a second.
3. Run the backfill to its end.
4. Compare the plays the broker received with the plays the run
   counted.

The test below does these steps against the fake broker in
`retainbroker_test.go` and the fake Jellyfin of the backfill tests. On
the code as of 2026-11-01 it fails with "the broker received 65 plays,
and the run counted 128 published". The test lives in this file until a
fix lands, and then it moves into the package with the fix.

```go
package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestABackfillPublishesEveryPlayWhileTheBrokerFallsBehind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		backfill.publish = func(topic string, payload []byte, retained bool) { backfill.bus.Publish(topic, payload, retained) }
		release := broker.holdReads()
		go func() {
			time.Sleep(500 * time.Millisecond)
			release()
		}()

		counts, err := backfill.runOnBus(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		broker.mutex.Lock()
		received := 0
		for _, topic := range broker.published {
			if strings.Contains(topic, "/outside") {
				received++
			}
		}
		broker.mutex.Unlock()
		if received != counts.published {
			t.Errorf("the broker received %d plays, and the run counted %d published", received, counts.published)
		}
	})
}
```

## Status

The remedy needs a design decision, which this document does not make.
The plays are not retained, so no connect or later pass can send a
dropped play again from what the broker or the backfill holds.
