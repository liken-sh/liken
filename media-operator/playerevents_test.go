package main

// A fault that each pass retries posts one Event for the first failure
// and one for the recovery, not one for each retry.

import (
	"testing"
	"testing/synctest"
)

// A screen with no Display is a Warning, because a person must fix it.
// A panel on another input is Normal, because the Player CRD documents
// it as expected.
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
				player.Status.Screen = earlierScreen()
				cluster.players["theater"] = player
				media := testOperator(t, cluster, make(chan struct{}, 1))

				runPlayers(media, []Player{*player}, nil)

				mustMatchAll(t, postedOn(cluster, "Player", "theater"), []string{each.want})
			})
		})
	}
}

func TestAScreenPostsOnlyWhenItChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := screenCluster()
		player := housePlayer()
		player.Status.Screen = earlierScreen()
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

// reasonsOn is postedOn without the messages, for Events whose message
// holds an error's text.
func reasonsOn(cluster *fakeCluster, kind, name string) []string {
	synctest.Wait()
	var posted []string
	for _, event := range cluster.events.About(kind, name) {
		posted = append(posted, event.Type+" "+event.Reason)
	}
	return posted
}

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

func TestAReceiverWritePostsItsFirstFailureAndItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		cluster.sessionsFail = true
		media := testOperator(t, cluster, make(chan struct{}, 1))

		runPlayers(media, []Player{*housePlayer()}, runningPlays())
		runPlayers(media, []Player{*housePlayer()}, runningPlays())
		cluster.sessionsFail = false
		runPlayers(media, []Player{*housePlayer()}, runningPlays())

		mustMatchAll(t, reasonsOn(cluster, "Player", "theater"), []string{
			"Warning ReceiverWriteFailed",
			"Normal Present",
			"Normal ReceiverWriteRecovered",
		})
	})
}
