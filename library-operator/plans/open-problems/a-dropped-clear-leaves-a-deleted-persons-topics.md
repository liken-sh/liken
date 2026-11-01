# A dropped clear leaves a deleted Person's topics on the broker

When a person deletes a `Person`, a new `Person` with the same name can
lose its progress later. The cause is a clear that `library-operator`
publishes once and never again when its bus client drops it.

## What happens

`library-operator` holds `library.liken.sh/progress` on a deleting
`Person` until every namespace's progress role answers the retained
forget request on `people/{person}/forget`. When every store has
answered, `forgetPerson` in `people.go` releases the finalizer, and then
`clearTopic` publishes an empty retained payload on the forget topic and
on each `forgotten` answer.

`Bus.Publish` drops a frame when its write queue of `busQueueDepth`
frames is full, for example while the broker falls behind on a burst of
many topics, and it drops every frame while the client has no session.
`clearTopic` publishes once. The `Person` is gone after the release, so
no later pass reads it, and nothing publishes the clear again. The
broker keeps the forget request.

A progress role subscribes to `people/+/forget` and deletes the rows of
each person that a forget request names. It reads every retained request
again each time it subscribes: when its pod starts and when it
reconnects to the broker.

## What a user sees

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

## How to reproduce it

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
`clearTopic` cannot keep its record until the bus queues the clear,
because no later pass calls it for the same object, and the record of a
forget request makes the next connect publish that request again.
