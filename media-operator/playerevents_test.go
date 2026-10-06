package main

// These tests cover the Events on a Player and a Remote, and the
// Events of a run's DisplayAlive condition: each condition transition
// posts one Event with the condition's reason and message, and a fault
// that a pass retries posts its first failure and its recovery. Each
// runs in a synctest bubble and reads the Events with postedOn.

import (
	"testing"
	"testing/synctest"
)

// The Screen condition's transitions post its reason and the
// Display's message. A screen with no Display is a Warning, because a
// person fixes it. A panel on another input is Normal, because the
// CRD describes that park as by design.
func TestAScreenTransitionPostsTheConditionsReason(t *testing.T) {
	cases := []struct {
		name    string
		display *Display
		want    string
	}{
		{name: "a lit panel", display: litDisplay(), want: "Normal Present: panel on DP-1"},
		{name: "a panel on another input", display: awayDisplay(), want: "Normal PanelAway: no panel on DP-1"},
		{name: "no Display", want: "Warning NoDisplay: no Display named DP-1"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := screenCluster()
				delete(cluster.displays, testMonitor)
				if each.display != nil {
					cluster.displays[testMonitor] = each.display
				}
				player := housePlayer()
				player.Status.Screen = rememberedScreen()
				cluster.players["theater"] = player
				media := testOperator(t, cluster, make(chan struct{}, 1))

				runPlayers(media, []Player{*player}, nil)

				mustMatchAll(t, postedOn(cluster, "Player", "theater"), []string{each.want})
			})
		})
	}
}

// A screen that stays as it was posts nothing more, and one that
// flips posts the new reason.
func TestAScreenPostsOnlyWhenItFlips(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := screenCluster()
		player := housePlayer()
		player.Status.Screen = rememberedScreen()
		cluster.players["theater"] = player
		media := testOperator(t, cluster, make(chan struct{}, 1))

		runPlayers(media, []Player{*cluster.players["theater"]}, nil)
		runPlayers(media, []Player{*cluster.players["theater"]}, nil)
		cluster.displays[testMonitor] = awayDisplay()
		runPlayers(media, []Player{*cluster.players["theater"]}, nil)

		mustMatchAll(t, postedOn(cluster, "Player", "theater"), []string{
			"Normal Present: panel on DP-1",
			"Normal PanelAway: no panel on DP-1",
		})
	})
}

// A display sidecar that went down posts its DisplayAlive transition
// as a Warning, with the restart count and the last exit.
func TestADisplayThatRestartsPostsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := runningCluster(housePlayer())
		dying := displayPod(1, false, &ContainerStateTerminated{ExitCode: 1, Reason: "Error"})
		dying.Metadata = cluster.pods["movie-playback"].Metadata
		dying.Spec = cluster.pods["movie-playback"].Spec
		cluster.pods["movie-playback"] = dying
		media := testOperator(t, cluster, make(chan struct{}, 1))

		media.pass()

		mustMatchAll(t, postedOn(cluster, "Play", "movie"), []string{
			"Warning Restarting: " + displayRestartMessage(dying.Status.InitContainerStatuses[0]),
		})
	})
}

// reasonsOn is postedOn with the type and the reason of each Event
// alone, for an Event whose message holds an error's own words.
func reasonsOn(cluster *fakeCluster, kind, name string) []string {
	synctest.Wait()
	var posted []string
	for _, event := range cluster.events.About(kind, name) {
		posted = append(posted, event.Type+" "+event.Reason)
	}
	return posted
}

// A Keymap that does not compile posts one Warning on the Remote, for
// as long as the same refusal stands.
func TestARefusedKeymapPostsOnceOnTheRemote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		media := testOperator(t, cluster, make(chan struct{}, 1))
		broken := testKeymap()
		broken.Spec.Buttons = []KeymapButton{{Press: "BTN_NOPE", Key: "KEY_UP"}}
		keymaps := map[string]*Keymap{"gamepad": broken}

		media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})
		media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})

		mustMatchAll(t, reasonsOn(cluster, "Remote", "sofa"), []string{"Warning KeymapRefused"})
	})
}

// A session write on the Receiver that fails posts a Warning once, the
// passes that write it again post nothing, and the write that lands
// posts the recovery.
func TestAReceiverWritePostsItsFirstFailureAndItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		cluster.sessionsFail = true
		media := testOperator(t, cluster, make(chan struct{}, 1))

		runPlayers(media, []Player{*housePlayer()}, standingPlays())
		runPlayers(media, []Player{*housePlayer()}, standingPlays())
		cluster.sessionsFail = false
		runPlayers(media, []Player{*housePlayer()}, standingPlays())

		mustMatchAll(t, reasonsOn(cluster, "Player", "theater"), []string{
			"Warning ReceiverWriteFailed",
			"Normal Present",
			"Normal ReceiverWriteRecovered",
		})
	})
}
