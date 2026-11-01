package main

// What this test proves: the record of a sent mark is retained on the broker
// again after the broker restarts. The bus runs with persistence off, so a
// restart drops every record, and the media browser publishes its marks
// again when its session starts. A role that started after both would read
// a mark with no record, and Jellyfin's last played date cannot stop it when
// a person toggled the title unplayed since, so the role would send the mark
// a second time and undo that toggle.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// jellyfinRoleOnABroker is one role over a fake Jellyfin, subscribed through
// its own bus to the broker. Its bus runs until the cancel it answers.
func jellyfinRoleOnABroker(t *testing.T, fake *fakeJellyfin, broker *retainBroker) (*jellyfin, context.CancelFunc) {
	t.Helper()
	role := newJellyfinOn("house", defaultTopicBase, defaultMediaTopicBase, "127.0.0.1:0",
		standJellyfinServer(t, fake), newJellyfinClock().now, nil, nil)
	role.index.prime(t.Context())
	role.joinBus("pipe")
	role.bus.dial = broker.dial
	role.subscribe()
	running, stop := context.WithCancel(t.Context())
	go role.bus.Run(running)
	return role, stop
}

func TestARoleThatStartsAfterABrokerRestartDoesNotSendAMarkAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := jellyfinFixture()
		broker := newRetainBroker(t)
		mark := jellyfinMark(t, markWatched, []string{"person-a"}, 8160)
		broker.retain(markTopic("mark-living-room-1"), mark)
		first, stopFirst := jellyfinRoleOnABroker(t, fake, broker)
		synctest.Wait()
		markPass(t, first)
		synctest.Wait()

		broker.restart()
		time.Sleep(2 * busMinBackoff)
		synctest.Wait()
		broker.retain(markTopic("mark-living-room-1"), mark)
		fake.userData = map[string]jellyfinUserData{"user-a/item-film": {Played: false}}
		stopFirst()
		synctest.Wait()
		second, _ := jellyfinRoleOnABroker(t, fake, broker)
		synctest.Wait()
		markPass(t, second)

		if len(fake.writes) != 1 {
			t.Errorf("writes = %+v, want the first role's one", fake.writes)
		}
	})
}
