package main

// What these tests prove: a retained mark that the bus dropped because its
// write queue was full goes out again on a later pass. A broker that falls
// behind stops reading, the client's queue fills, and Publish drops each
// frame past it. A pass that recorded such a mark as published would skip it
// on every later pass, and the broker would never hold it.

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// manyMarks is half again as many marks as the bus's write queue holds,
// each on its own topic, so one burst overflows the queue and the second
// burst holds what the first left over.
func manyMarks() map[string]personForgetRequest {
	marks := map[string]personForgetRequest{}
	for index := range busQueueDepth * 3 / 2 {
		topic := personForgetTopic(defaultTopicBase, fmt.Sprintf("person-%d", index))
		marks[topic] = personForgetRequest{At: "2026-11-01T00:00:00Z"}
	}
	return marks
}

// unretained counts the topics of marks that the broker holds no message on.
func unretained(broker *retainBroker, marks map[string]personForgetRequest) int {
	missing := 0
	for topic := range marks {
		if _, held := broker.retainedOn(topic); !held {
			missing++
		}
	}
	return missing
}

func TestAMarkDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, broker := operatorOnARetainBroker(t, newFakeCluster())
		release := broker.holdReads()
		go operator.bus.Run(t.Context())
		synctest.Wait()
		marks := manyMarks()
		for topic, mark := range marks {
			operator.publishMark(topic, mark)
		}
		synctest.Wait()

		release()
		synctest.Wait()
		for topic, mark := range marks {
			operator.publishMark(topic, mark)
		}
		synctest.Wait()

		if missing := unretained(broker, marks); missing > 0 {
			t.Errorf("the broker retains nothing on %d of %d topics", missing, len(marks))
		}
	})
}

func TestAMarkDroppedOnAFullQueueDuringARepublishIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, broker := operatorOnARetainBroker(t, newFakeCluster())
		go operator.bus.Run(t.Context())
		synctest.Wait()
		marks := manyMarks()
		for topic, mark := range marks {
			operator.publishMark(topic, mark)
			synctest.Wait()
		}

		release := broker.holdReads()
		broker.restart()
		time.Sleep(2 * busMinBackoff)
		synctest.Wait()
		operator.pass()
		synctest.Wait()
		release()
		synctest.Wait()
		operator.pass()
		synctest.Wait()

		if missing := unretained(broker, marks); missing > 0 {
			t.Errorf("the broker retains nothing on %d of %d topics", missing, len(marks))
		}
	})
}
