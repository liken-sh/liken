package main

// These tests cover a screen that goes away for a short time. The pod
// that draws the idle screen holds the idle claim, and the claim loses
// its allocation while that pod is replaced. The unit keeps its
// Receiver match through such a gap, and loses it when the screen stays
// away past the bound.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// gapStart is the moment each test's clock starts at.
var gapStart = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// gapOperator builds an operator over a unit wired through a Receiver,
// with the idle screen on and a clock the test moves. The idle claim
// carries the stamp the pass builds, so the pass keeps it and a test
// can take its allocation away and give it back.
func gapOperator(t *testing.T) (*operator, *fakeCluster, *testClock) {
	t.Helper()
	cluster := receiverCluster()
	claim := cluster.claims[idleClaimName("theater")]
	if err := stampTemplateHash(&claim.Metadata, claim.Spec); err != nil {
		t.Fatal(err)
	}
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	clock := &testClock{now: gapStart}
	media.now = clock.read
	return media, cluster, clock
}

// runGap runs one pass with the idle claim unallocated at the given
// offset from the start of the gap, and answers the power topic the
// Player's status carries after it.
func runGap(media *operator, cluster *fakeCluster, clock *testClock, offset time.Duration) string {
	cluster.claims[idleClaimName("theater")].Status = nil
	clock.now = gapStart.Add(offset)
	runPlayers(media, []Player{*housePlayer()}, nil)
	return statusPowerTopic(cluster)
}

// statusPowerTopic reads the power topic off the idle bus block, or the
// word none for a status with no topic.
func statusPowerTopic(cluster *fakeCluster) string {
	status := cluster.players["theater"].Status
	if status.Idle == nil || status.Idle.Bus == nil || status.Idle.Bus.PowerTopic == "" {
		return "none"
	}
	return status.Idle.Bus.PowerTopic
}

// A screen pod that another operator replaces leaves the idle claim
// unallocated until the scheduler places the new pod. That operator
// builds the power topic into the pod's template, so a topic that
// changed during the gap would make it replace the pod again, and each
// lift and apply would turn the equipment off and on. A gap within the
// bound changes nothing. A screen that stays away past the bound lifts
// the session and drops the topic.
func TestAScreenGapKeepsTheReceiverMatchOnlyWithinTheBound(t *testing.T) {
	topic := playerPowerTopic(defaultTopicBase, "house", "theater")
	cases := []struct {
		name        string
		offsets     []time.Duration
		wantTopics  string
		wantApplies string
	}{
		{
			name:        "the screen comes back within the bound",
			offsets:     []time.Duration{0, screenGapBound - time.Second},
			wantTopics:  topic + ", " + topic + ", " + topic,
			wantApplies: "den-receiver: GAME",
		},
		{
			name:        "the screen stays away past the bound",
			offsets:     []time.Duration{0, screenGapBound},
			wantTopics:  topic + ", none, " + topic,
			wantApplies: "den-receiver: GAME, den-receiver: lift, den-receiver: GAME",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster, clock := gapOperator(t)

			runPlayers(media, []Player{*housePlayer()}, nil)
			before := statusPowerTopic(cluster)
			allocated := cluster.claims[idleClaimName("theater")].Status
			var gap string
			for _, offset := range each.offsets {
				gap = runGap(media, cluster, clock, offset)
			}
			cluster.claims[idleClaimName("theater")].Status = allocated
			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, strings.Join([]string{before, gap, statusPowerTopic(cluster)}, ", "), each.wantTopics)
			mustMatch(t, appliedSessions(cluster), each.wantApplies)
		})
	}
}

// A screen that resolves again ends the gap, so the next gap has the
// whole bound again.
func TestAScreenThatComesBackStartsTheNextGapAfresh(t *testing.T) {
	media, cluster, clock := gapOperator(t)

	runPlayers(media, []Player{*housePlayer()}, nil)
	allocated := cluster.claims[idleClaimName("theater")].Status
	runGap(media, cluster, clock, 0)
	cluster.claims[idleClaimName("theater")].Status = allocated
	runPlayers(media, []Player{*housePlayer()}, nil)
	runGap(media, cluster, clock, screenGapBound-time.Second)
	gap := runGap(media, cluster, clock, screenGapBound+time.Second)

	mustMatch(t, gap, playerPowerTopic(defaultTopicBase, "house", "theater"))
	mustMatch(t, appliedSessions(cluster), "den-receiver: GAME")
}

// A unit that never resolved a screen in this run has no match to keep,
// so an unallocated claim matches no Receiver from the first pass.
func TestAScreenThatNeverResolvedMatchesNothing(t *testing.T) {
	media, cluster, _ := gapOperator(t)
	cluster.claims[idleClaimName("theater")].Status = nil

	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, statusPowerTopic(cluster), "none")
	mustMatch(t, appliedSessions(cluster), "")
}

// A Player that is gone takes its held screen with it, so a Player
// created again under the same name does not match through a gap it
// never had.
func TestAPlayerThatIsGoneForgetsItsScreen(t *testing.T) {
	media, cluster, _ := gapOperator(t)

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)
	cluster.claims[idleClaimName("theater")].Status = nil
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, statusPowerTopic(cluster), "none")
	mustMatch(t, appliedSessions(cluster), "den-receiver: GAME, den-receiver: lift")
}

// The first pass of a gap wakes the pass again at the bound, so a
// screen that is really gone loses its match when the bound ends, and
// not at the next unrelated event.
func TestAScreenGapWakesThePassAtTheBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)
		cluster := receiverCluster()
		media := testOperator(t, cluster, wake)

		runPlayers(media, []Player{*housePlayer()}, nil)
		cluster.claims[idleClaimName("theater")].Status = nil
		runPlayers(media, []Player{*housePlayer()}, nil)
		synctest.Wait()
		received(wake)
		time.Sleep(screenGapBound - time.Second)
		synctest.Wait()
		mustMatch(t, received(wake), false)
		time.Sleep(time.Second)
		synctest.Wait()

		mustMatch(t, received(wake), true)
	})
}
