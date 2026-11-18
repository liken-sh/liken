package main

import (
	"fmt"
	"io"
	"testing"
	"testing/synctest"
)

// statusTopics returns more topics than busQueueDepth, so one burst of
// publishes fills the bus's write queue and Publish drops the rest. The
// fake broker stops reading when its pubs channel is full, so the queue
// stays full until the test starts to read pubs.
func statusTopics() []string {
	topics := make([]string, 0, busQueueDepth*3/2)
	for index := range cap(topics) {
		topics = append(topics, playerStatusTopic(defaultTopicBase, "living-room", fmt.Sprintf("player-%d", index)))
	}
	return topics
}

// publishIdle publishes an idle status to each topic, as a reconcile pass does.
func publishIdle(t *testing.T, statuses *publishedStatuses, bus *Bus, topics []string) {
	t.Helper()
	for _, topic := range topics {
		if _, _, err := statuses.publishDerived(bus, topic, func() ([]byte, string, error) {
			return []byte(`{"state":"idle"}`), "idle", nil
		}); err != nil {
			t.Fatalf("publishing the idle status to %s: %v", topic, err)
		}
	}
}

// A Player status that Publish dropped on a full queue is published by
// the next pass. If publishedStatuses recorded the dropped status as
// published, every later pass would skip it.
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

// publishTables publishes the base key table of count Remotes, as a
// reconcile pass does, and returns the topic of each table.
func publishTables(media *operator, count int) []string {
	topics := make([]string, 0, count)
	for index := range count {
		remote := &Remote{Metadata: ObjectMeta{Namespace: "living-room", Name: fmt.Sprintf("remote-%d", index)}}
		media.publishKeys(remote, nil, map[string]bool{})
		topics = append(topics, remoteKeysTopic(defaultTopicBase, "living-room", remote.Metadata.Name))
	}
	return topics
}

// A key table that Publish dropped on a full queue is published by the
// next pass.
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
