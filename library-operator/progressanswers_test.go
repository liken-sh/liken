package main

// What this test proves: the progress role's answers reach the broker when
// the role answers more messages in one burst than its bus's write queue
// holds. The operator holds a Play's finalizer until it reads the role's
// recorded mark, and the role publishes that mark once, so a mark that the
// bus dropped would hold the Play until the role connected again.

import (
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// roleOnABroker is the house namespace's progress role over a store in
// SQLite, with its bus connected to the broker.
func roleOnABroker(t *testing.T, broker *retainBroker) *progress {
	t.Helper()
	server := apiservertest.Start(t, &sqliteAgent{db: newSQLiteProgress(t)})
	role := newProgressOn("house", defaultTopicBase, defaultMediaTopicBase,
		newProgressStore(apiservertest.Host, server.Client()), io.Discard)
	role.bus = newBus(testBusAddress, "progress-house", nil, role.onConnect, role.onMessage)
	role.bus.dial = broker.dial
	go role.bus.Run(t.Context())
	synctest.Wait()
	return role
}

// A broker that falls behind stops reading, and the role's answers back up
// into its write queue. The role reads the finals of many Plays in one
// burst when it subscribes after a restart.
func TestTheRoleAnswersEveryFinalOfABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		broker := newRetainBroker(t)
		role := roleOnABroker(t, broker)
		plays := 2 * busQueueDepth
		release := broker.holdReads()
		go func() {
			time.Sleep(500 * time.Millisecond)
			release()
		}()

		for index := range plays {
			role.onMessage(playFinalTopic(defaultTopicBase, "house", fmt.Sprintf("play-%d", index)),
				[]byte(`{"phase":"Finished","item":1,"position":"0:59:59","duration":"1:00:00"}`))
		}
		time.Sleep(time.Second)
		synctest.Wait()

		missing := 0
		for index := range plays {
			if _, held := broker.retainedOn(playRecordedTopic(defaultTopicBase, "house", fmt.Sprintf("play-%d", index))); !held {
				missing++
			}
		}
		if missing > 0 {
			t.Errorf("the broker holds no recorded mark for %d of %d plays", missing, plays)
		}
	})
}
