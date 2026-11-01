package main

// What this test proves: a record of a sent mark that the bus dropped
// because its write queue was full goes out again on the next pass. A
// connect publishes every record in one burst, and a record the broker never
// holds lets a role that starts later send its mark a second time.

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestARecordDroppedOnAFullQueueIsPublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		broker := newRetainBroker(t)
		role, _ := jellyfinRoleOnABroker(t, jellyfinFixture(), broker)
		synctest.Wait()
		names := make([]string, 0, busQueueDepth*3/2)
		for index := range cap(names) {
			name := fmt.Sprintf("mark-living-room-%d", index)
			names = append(names, name)
			role.onMessage(sentTopic(name), sentRecord(t, markPressed))
		}
		release := broker.holdReads()
		broker.restart()
		time.Sleep(2 * busMinBackoff)
		synctest.Wait()

		release()
		synctest.Wait()
		markPass(t, role)
		synctest.Wait()

		missing := 0
		for _, name := range names {
			if _, held := broker.retainedOn(sentTopic(name)); !held {
				missing++
			}
		}
		if missing > 0 {
			t.Errorf("the broker retains no record for %d of %d marks", missing, len(names))
		}
	})
}
