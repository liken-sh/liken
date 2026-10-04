package main

// These tests cover the power mode the unit's retained status states:
// room while the unit's screen is wired through a Receiver, and screen
// while the screen client handles a power press itself. Every place
// that publishes the status states the same mode. An operator that
// restarts states no mode for a unit until its first pass reaches the
// unit, so a wired unit whose screen resolves does not read screen on
// the way to room. A unit whose idle claim is unallocated still reads
// screen until its screen resolves (screengap.go).

import (
	"encoding/json"
	"testing"
)

// The three places that publish a unit's status: the start of a pass,
// the bus reader's answer to an ending, and the end of a pass. Each one
// reads the mode from the ensure desk, so each states the mode the last
// pass placed.
func TestEveryPublishStatesThePowerMode(t *testing.T) {
	sites := []struct {
		name    string
		publish func(*operator)
	}{
		{name: "the start of a pass", publish: func(media *operator) {
			media.publishPlayerStatuses([]Player{*housePlayer()}, nil)
		}},
		{name: "the answer to an ending", publish: func(media *operator) {
			media.snapshot.recordPlays([]Play{*housePlay("https://nas/film.mkv")})
			media.snapshot.recordPlayers([]Player{*housePlayer()})
			media.answerEnding("house", "movie")
		}},
		{name: "the end of a pass", publish: func(media *operator) {
			runPlayers(media, []Player{*housePlayer()}, nil)
		}},
	}
	wirings := []struct {
		name    string
		cluster func() *fakeCluster
		want    string
	}{
		{name: "with a receiver", cluster: receiverCluster, want: powerRoom},
		{name: "with no receiver", cluster: screenCluster, want: powerScreen},
	}
	for _, site := range sites {
		for _, wiring := range wirings {
			t.Run(site.name+" "+wiring.name, func(t *testing.T) {
				media := testOperator(t, wiring.cluster(), make(chan struct{}, 1))
				media.idleDisplayClass = "display-draw"
				runPlayers(media, []Player{*housePlayer()}, nil)
				media.playerStatuses.reset()

				site.publish(media)

				mustMatch(t, publishedPower(t, media), wiring.want)
			})
		}
	}
}

// An operator that starts holds an empty ensure desk until its first
// pass reads the Receivers, and the pass publishes before that read.
// The early publish states no mode, which a client reads as the mode it
// already holds, so a unit whose screen resolves reads its own mode next
// and never the other one on the way.
func TestARestartStatesNoModeAndThenTheUnitsMode(t *testing.T) {
	cases := []struct {
		name    string
		cluster func() *fakeCluster
		want    string
	}{
		{name: "a unit wired through a receiver", cluster: receiverCluster, want: powerRoom},
		{name: "a unit straight into a panel", cluster: screenCluster, want: powerScreen},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, broker := busOperator(t, each.cluster())
			media.idleDisplayClass = "display-draw"

			media.pass()

			mustMatchAll(t, publishedPowers(t, broker, media.bus), []string{"none", each.want})
		})
	}
}

// A deleted Player leaves the ensure desk, so a Player created again
// under its name states no mode at the start of the next pass, and not
// the mode of the unit that was deleted.
func TestAPlayerCreatedAgainStatesNoStaleMode(t *testing.T) {
	media := testOperator(t, receiverCluster(), make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)

	media.publishPlayerStatuses([]Player{*housePlayer()}, nil)

	mustMatch(t, publishedPower(t, media), "none")
}

// publishedPowers reads every publish the operator queued, up to a mark
// the test publishes itself, and answers the power mode of each status
// the house unit published, in order, with none for a status that
// states no mode. The Bus writes one connection through one queue in
// order, so the mark arrives behind everything the pass queued.
func publishedPowers(t *testing.T, broker *fakeBroker, bus *Bus) []string {
	t.Helper()
	const mark = "liken/media/test/powers-read"
	bus.Publish(mark, []byte("read me"), false)
	topic := playerStatusTopic(defaultTopicBase, "house", "theater")
	var powers []string
	for {
		published := waitForPublish(t, broker.pubs)
		if published.topic == mark {
			return powers
		}
		if published.topic != topic {
			continue
		}
		var status playerBusStatus
		mustSucceed(t, json.Unmarshal(published.payload, &status))
		if status.Power == "" {
			status.Power = "none"
		}
		powers = append(powers, status.Power)
	}
}
