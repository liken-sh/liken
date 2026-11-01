package main

// What these tests prove: a Player status or a key table that the bus
// dropped because its write queue was full goes out again on a later pass.
// The fake broker stops reading while no one takes its publishes, so the
// client's queue fills and Publish drops each frame past it. A record of
// such a payload would skip it on every later pass, and the broker would
// never hold it.

import (
	"fmt"
	"io"
	"testing"
	"testing/synctest"
)

// statusTopics is half again as many status topics as the bus's write queue
// holds, so one burst overflows the queue and the second burst holds what
// the first left over.
func statusTopics() []string {
	topics := make([]string, 0, busQueueDepth*3/2)
	for index := range cap(topics) {
		topics = append(topics, playerStatusTopic(defaultTopicBase, "living-room", fmt.Sprintf("player-%d", index)))
	}
	return topics
}

// publishIdle publishes one idle status on each topic, as a pass does.
func publishIdle(t *testing.T, statuses *publishedStatuses, bus *Bus, topics []string) {
	t.Helper()
	for _, topic := range topics {
		if _, _, err := statuses.publishDerived(bus, topic, func() ([]byte, string, error) {
			return []byte(`{"state":"idle"}`), "idle", nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAStatusDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		statuses := &publishedStatuses{}
		topics := statusTopics()
		publishIdle(t, statuses, bus, topics)
		synctest.Wait()

		received := map[string]bool{}
		go func() {
			for {
				select {
				case published := <-brokers[0].pubs:
					received[published.topic] = true
				case <-t.Context().Done():
					return
				}
			}
		}()
		synctest.Wait()
		publishIdle(t, statuses, bus, topics)
		synctest.Wait()

		mustMatch(t, len(received), len(topics))
	})
}

// publishTables publishes the base key table for each of count Remotes, as
// a pass does, and answers each table's topic.
func publishTables(media *operator, count int) []string {
	topics := make([]string, 0, count)
	for index := range count {
		remote := &Remote{Metadata: ObjectMeta{Namespace: "living-room", Name: fmt.Sprintf("remote-%d", index)}}
		media.publishKeys(remote, nil, map[string]bool{})
		topics = append(topics, remoteKeysTopic(defaultTopicBase, "living-room", remote.Metadata.Name))
	}
	return topics
}

func TestAKeyTableDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, brokers, connected := startBus(t, 1, nil, nil)
		waitForConnect(t, connected)
		media := &operator{bus: bus, topicBase: defaultTopicBase,
			keysPublished: map[string]string{}, log: io.Discard}
		topics := publishTables(media, busQueueDepth*3/2)
		synctest.Wait()

		received := map[string]bool{}
		go func() {
			for {
				select {
				case published := <-brokers[0].pubs:
					received[published.topic] = true
				case <-t.Context().Done():
					return
				}
			}
		}()
		synctest.Wait()
		publishTables(media, len(topics))
		synctest.Wait()

		mustMatch(t, len(received), len(topics))
	})
}
