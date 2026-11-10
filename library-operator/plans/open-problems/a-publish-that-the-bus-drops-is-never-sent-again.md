# A publish that the bus drops is never sent again

Two parts of `library-operator` publish a message on the bus once and
ignore the answer of the publish. When the bus client drops that
message, nothing sends it again. A Jellyfin backfill then loses plays,
and the deletion of a `Person` leaves its forget request on the broker,
where it can later delete the progress of a new `Person` with the same
name.

## The shared cause

`Bus.Publish` (`bus.go:258`) answers whether the frame reached the
client's write queue. It drops a frame when its write queue of
`busQueueDepth` frames (64, `bus.go:33`) is full, for example while the
broker reads slower than the client publishes, and it drops every frame
while the client has no session. A caller that skips a payload it
already published records the payload only on `true`, so a frame that a
full queue dropped goes out again on its next try.

Two callers publish once and do not read the answer:

- The backfill's `carry` (`jellyfinbackfill.go:214`) publishes each play
  through `b.publish` at `jellyfinbackfill.go:228`. The field's type,
  `func(topic string, payload []byte, retained bool)`
  (`jellyfinbackfill.go:77`), has no result, so `carry` cannot read the
  answer, and it counts the play as published.
- `clearTopic` (`storemarks.go:232`) deletes the topic from
  `o.published` and `o.unsent`, and then calls `o.bus.Publish` once at
  `storemarks.go:235`.

## A Jellyfin backfill loses plays to a full queue

A Jellyfin backfill can report that it published every play while the
progress store never receives some of them.

### What happens

The backfill Job (`jellyfinbackfill.go`) reads each user's resumable and
played items from Jellyfin, and publishes each one as an outside play on
`plays/{namespace}/{name}/outside`. An outside play is not retained,
the way the webhook path publishes it, so the progress role records it
only if the role reads it as it arrives. The backfill waits
`jellyfinBackfillPace` between two publishes so that the client's writer
keeps its queue short.

While the broker reads slower than the backfill publishes, the queue
fills, and each later play is dropped. `carry` ignores the answer of the
publish and counts the play as published. The run ends with no error,
so the Job succeeds and the operator does not run it again. The broker
holds no copy of a dropped play, and nothing publishes it again.

The jellyfin role's reconcile (`jellyfinreconcile.go`) carries each item
through the same code when the progress role reports online, so it can
drop plays the same way.

### What a user sees

After a backfill that reported success, some plays are missing from the
progress store. The Job's log line counts every play as published, for
example "backfilled 1 users: 128 plays published, 0 items skipped". The
titles behind the
missing plays show no resume point and no watched mark on the screens,
although Jellyfin holds both.

### How to reproduce it

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

## A dropped clear leaves a deleted Person's topics on the broker

When a person deletes a `Person`, a new `Person` with the same name can
lose its progress later.

### What happens

`library-operator` holds `library.liken.sh/progress` on a deleting
`Person` until every namespace's progress role answers the retained
forget request on `people/{person}/forget`. When every store has
answered, `forgetPerson` in `people.go` releases the finalizer, and then
`clearTopic` publishes an empty retained payload on the forget topic and
on each `forgotten` answer.

`clearTopic` publishes once. The `Person` is gone after the release, so
no later pass reads it, and nothing publishes the clear again. The
broker keeps the forget request.

A progress role subscribes to `people/+/forget` and deletes the rows of
each person that a forget request names. It reads every retained request
again each time it subscribes: when its pod starts and when it
reconnects to the broker.

### What a user sees

A person deletes a `Person`, and later creates a new `Person` with the
same name. The new person's progress is recorded as usual. The next time
a progress pod starts or reconnects to the broker, it reads the old
forget request and deletes the new person's rows from that namespace's
store. Resume points and watched marks for that person disappear with no
action from anyone.

The clears of a released `Play` (`clearPlayTopics` in `plays.go`)
publish once after the finalizer is released, the same way. A dropped
one leaves the `Play`'s `audience`, `final`, and `recorded` topics on the
broker.

### How to reproduce it

1. Connect the operator to a broker and publish a forget request for one
   person.
2. Stop the broker reading, so the client's write queue fills.
3. Publish more marks than the queue holds, then clear the forget topic.
4. Let the broker read again, and run two passes.
5. The broker still retains the forget request.

The test below does these steps against the fake broker in
`retainbroker_test.go`, and it fails on the code as of 2026-11-01 with
"the broker still retains liken/library/people/person-gone/forget after
the clear". The test lives in this file until a fix lands, and then it
moves into the package with the fix.

```go
package main

import (
	"testing"
	"testing/synctest"
)

func TestAClearDroppedOnAFullQueueClearsTheTopicOnALaterPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, broker := operatorOnARetainBroker(t, newFakeCluster())
		go operator.bus.Run(t.Context())
		synctest.Wait()
		topic := personForgetTopic(defaultTopicBase, "person-gone")
		operator.publishMark(topic, personForgetRequest{At: "2026-11-01T00:00:00Z"})
		synctest.Wait()

		release := broker.holdReads()
		for mark := range manyMarks() {
			operator.publishMark(mark, personForgetRequest{At: "2026-11-01T00:00:00Z"})
		}
		operator.clearTopic(topic)
		release()
		synctest.Wait()
		operator.pass()
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if _, held := broker.retainedOn(topic); held {
			t.Errorf("the broker still retains %s after the clear", topic)
		}
	})
}
```

## Status

The remedy needs a design decision, which this document does not make.
It is one decision for both failures, because both come from a publish
that is sent once and whose answer nobody reads.

- The backfill's plays are not retained, so no connect or later pass can
  send a dropped play again from what the broker or the backfill holds.
- `clearTopic` cannot keep its record until the bus queues the clear,
  because no later pass calls it for the same object, and the record of
  a forget request makes the next connect publish that request again.
