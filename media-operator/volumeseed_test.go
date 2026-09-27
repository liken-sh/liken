package main

// These tests cover the seed after a broker that restarted alone. The
// broker keeps no retained value across a restart, and the operator still
// holds each unit's level on its desk, so the pass publishes that level
// again instead of leaving the topic empty.

import (
	"strings"
	"testing"
)

// brokerRestarted is a broker session that begins with no retained
// value, after the unit's level had reached the operator on the last
// session.
func brokerRestarted(t *testing.T, owned bool) (*operator, *fakeBroker) {
	t.Helper()
	media, broker := caughtUpOperator(t, newFakeCluster())
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":30,"muted":true}`))
	if owned {
		media.handleBusMessage(theaterVolumeOwnerTopic(), []byte(`{"owner":"receiver/den"}`))
	}
	media.volumes.newSession()
	return media, broker
}

// An idle unit's level lives on the broker and on the operator's desk.
// A broker that restarts loses its copy, so the pass publishes the
// level the desk holds. Without it, an operator that restarts next
// finds no level anywhere and seeds unity, and the next film plays at
// full volume.
func TestABrokerRestartGetsTheHeldLevelBack(t *testing.T) {
	media, broker := brokerRestarted(t, false)
	var log logBuffer
	media.log = &log

	media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)

	published := mustPublishVolume(t, broker)
	mustMatch(t, string(published.payload), `{"level":30,"muted":true}`)
	mustMatch(t, published.retained, true)
	mustMatch(t, strings.Join(linesAbout(&log, "player house/theater"), "\n"),
		"player house/theater: the broker held no level after the catch-up, published the held level 30, muted to "+theaterVolumeTopic())
}

// The republish happens once a session: the level it wrote is on the
// desk as a delivery of this session.
func TestTheHeldLevelIsPublishedOnceASession(t *testing.T) {
	media, broker := brokerRestarted(t, false)
	player := settledPlayer(housePlayer())

	media.reconcilePlayers([]Player{player}, nil, "", nil)
	mustPublishVolume(t, broker)
	media.reconcilePlayers([]Player{player}, nil, "", nil)

	mustPublishNoVolume(t, broker)
}

// A unit whose level equipment owns, and a unit with a standing Play,
// get no republish: the equipment and the playback pod each publish the
// level they hold on their own reconnect.
func TestAHeldLevelSomeoneElseHoldsIsNotPublished(t *testing.T) {
	cases := []struct {
		name  string
		owned bool
		plays []Play
	}{
		{name: "equipment owns the level", owned: true},
		{name: "a Play stands on the unit", plays: standingPlays()},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, broker := brokerRestarted(t, each.owned)

			media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, each.plays, "", nil)

			mustPublishNoVolume(t, broker)
		})
	}
}

// A level the new session delivers is the broker's own, so the pass
// writes nothing over it.
func TestALevelTheNewSessionDeliversIsNotPublished(t *testing.T) {
	media, broker := brokerRestarted(t, false)
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":30,"muted":true}`))

	media.reconcilePlayers([]Player{settledPlayer(housePlayer())}, nil, "", nil)

	mustPublishNoVolume(t, broker)
}
