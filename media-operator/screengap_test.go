package main

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

var gapStart = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// gapOperator builds an operator for a Player wired through a Receiver,
// with the idle screen on and a clock the test controls. The idle claim
// already has the template hash that the pass would write on it, so the
// pass does not replace the claim while the test removes and restores
// its allocation.
func gapOperator(t *testing.T) (*operator, *fakeCluster, *testClock) {
	t.Helper()
	cluster := receiverCluster()
	claim := cluster.claims[idleClaimName("theater")]
	if err := stampTemplateHash(&claim.Metadata, claim.Spec); err != nil {
		t.Fatalf("stamping the template hash on the claim: %v", err)
	}
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	clock := &testClock{now: gapStart}
	media.now = clock.read
	return media, cluster, clock
}

// runGap runs one pass at the given offset from the start of the gap,
// with the idle claim unallocated. It returns the Receiver that
// playerReceivers records for the Player after the pass.
func runGap(t *testing.T, media *operator, cluster *fakeCluster, clock *testClock, offset time.Duration) string {
	t.Helper()
	cluster.claims[idleClaimName("theater")].Status = nil
	clock.now = gapStart.Add(offset)
	runPlayers(media, []Player{*housePlayer()}, nil)
	return wiredReceiver(media)
}

// A gap shorter than the bound changes nothing: the session stays on
// the Receiver, and playerReceivers still records the Receiver, so a press
// during the gap still switches the input. A gap as long as the bound
// removes the session and the Receiver until the screen resolves again.
func TestAScreenGapKeepsTheReceiverMatchOnlyWithinTheBound(t *testing.T) {
	cases := []struct {
		name          string
		offsets       []time.Duration
		wantReceivers string
		wantApplies   string
	}{
		{
			name:          "the screen comes back within the bound",
			offsets:       []time.Duration{0, screenGapBound - time.Second},
			wantReceivers: "den-receiver, den-receiver, den-receiver",
			wantApplies:   "den-receiver: GAME",
		},
		{
			name:          "the screen stays away past the bound",
			offsets:       []time.Duration{0, screenGapBound},
			wantReceivers: "den-receiver, none, den-receiver",
			wantApplies:   "den-receiver: GAME, den-receiver: lift, den-receiver: GAME",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster, clock := gapOperator(t)

			runPlayers(media, []Player{*housePlayer()}, nil)
			before := wiredReceiver(media)
			allocated := cluster.claims[idleClaimName("theater")].Status
			var gap string
			for _, offset := range each.offsets {
				gap = runGap(t, media, cluster, clock, offset)
			}
			cluster.claims[idleClaimName("theater")].Status = allocated
			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, strings.Join([]string{before, gap, wiredReceiver(media)}, ", "), each.wantReceivers)
			mustMatch(t, appliedSessions(cluster), each.wantApplies)
		})
	}
}

// A screen that resolves again ends the gap, so the next gap gets the
// whole bound again.
func TestAScreenThatResolvesAgainRestartsTheBoundForTheNextGap(t *testing.T) {
	media, cluster, clock := gapOperator(t)

	runPlayers(media, []Player{*housePlayer()}, nil)
	allocated := cluster.claims[idleClaimName("theater")].Status
	runGap(t, media, cluster, clock, 0)
	cluster.claims[idleClaimName("theater")].Status = allocated
	runPlayers(media, []Player{*housePlayer()}, nil)
	runGap(t, media, cluster, clock, screenGapBound-time.Second)
	gap := runGap(t, media, cluster, clock, screenGapBound+time.Second)

	mustMatch(t, gap, "den-receiver")
	mustMatch(t, appliedSessions(cluster), "den-receiver: GAME")
}

// A Player that has not resolved a screen since the operator started
// has no last screen, so an unallocated claim matches no Receiver.
func TestAScreenThatNeverResolvedMatchesNothing(t *testing.T) {
	media, cluster, _ := gapOperator(t)
	cluster.claims[idleClaimName("theater")].Status = nil

	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, wiredReceiver(media), "none")
	mustMatch(t, appliedSessions(cluster), "")
}

// When a Player is deleted, the operator forgets its screen, so a
// Player created again under the same name does not match through a gap
// it never had.
func TestADeletedPlayerLeavesNoScreenForTheNextPlayerOfThatName(t *testing.T) {
	media, cluster, _ := gapOperator(t)

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)
	cluster.claims[idleClaimName("theater")].Status = nil
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, wiredReceiver(media), "none")
	mustMatch(t, appliedSessions(cluster), "den-receiver: GAME, den-receiver: lift")
}

// The first pass of a gap requeues a pass at the end of the bound.
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
