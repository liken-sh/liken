package main

// These tests check the operator's log lines: one line for each change
// the operator makes, and none for a pass that changes nothing.

import (
	"strings"
	"testing"
	"testing/synctest"
)

// loggingOperator is testOperator with its lines collected.
func loggingOperator(t *testing.T, cluster *fakeCluster) (*operator, *logBuffer) {
	t.Helper()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	var log logBuffer
	media.log = &log
	return media, &log
}

// linesAbout keeps the lines about one subject, so a test of a Play
// ignores the lines that the same pass writes about Remotes and Players.
func linesAbout(log *logBuffer, subject string) []string {
	var kept []string
	for _, line := range log.lines() {
		if strings.HasPrefix(line, subject+":") {
			kept = append(kept, line)
		}
	}
	return kept
}

func TestANewPlayLogsItsPodAndItsPhaseOnce(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media, log := loggingOperator(t, cluster)

	media.pass()
	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), []string{
		"play house/movie: created playback pod movie-playback on player theater at the start, because the play is new",
		"play house/movie: phase Pending, was none",
	})
}

func TestARefusedPlayLogsItsFailureOnce(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("ftp://nas/film.mkv")
	cluster.players["theater"] = housePlayer()
	media, log := loggingOperator(t, cluster)

	media.pass()
	media.pass()

	lines := linesAbout(log, "play house/movie")
	mustMatch(t, len(lines), 1)
	mustMatch(t, strings.HasPrefix(lines[0], "play house/movie: phase Failed, was none: "), true)
	mustMatch(t, strings.HasSuffix(lines[0], cluster.plays["movie"].Status.Message), true)
}

func TestAFinishedRunLogsTheEndOfItsRunOnce(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Status.Item = 1
	cluster.plays["movie"].Status.Position = "1:58:00"
	cluster.pods["movie-playback"].Status.Phase = podSucceeded
	media, log := loggingOperator(t, cluster)

	media.pass()
	media.pass()
	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), []string{
		"play house/movie: phase Finished, was Running",
		"play house/movie: finished at item 1, 1:58:00, deleted its playback pod and its claim",
	})
}

func TestAPlayPastItsWindowLogsItsDelete(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Status = PlayStatus{Phase: phaseFinished, FinishedAt: "2026-01-01T00:00:00Z"}
	media, log := loggingOperator(t, cluster)
	window := playTTL(cluster.plays["movie"])

	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), []string{
		"play house/movie: deleted, because " + window.String() + " passed after it finished",
	})
}

func TestARecreatedPodLogsWhy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCluster)
		want  string
	}{
		{
			name:  "a spec edit on the player",
			setup: func(cluster *fakeCluster) { cluster.players["theater"] = brightPlayer() },
			want:  "play house/movie: recreated playback pod movie-playback at 0:15:00, because a spec edit changed its devices on player theater",
		},
		{
			name: "a failed pod",
			setup: func(cluster *fakeCluster) {
				cluster.pods["movie-playback"].Status.Phase = podFailed
				cluster.pods["movie-playback"].Status.Message = "the node ran out of memory"
			},
			want: "play house/movie: recreated playback pod movie-playback at 0:15:00, because the pod failed: the node ran out of memory",
		},
		{
			name:  "a pod that is gone",
			setup: func(cluster *fakeCluster) { delete(cluster.pods, "movie-playback") },
			want:  "play house/movie: created playback pod movie-playback on player theater at 0:15:00, because the run had no pod",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := runningCluster(housePlayer())
			each.setup(cluster)
			media, log := loggingOperator(t, cluster)
			media.reports.fold("house", "movie", playReport{Item: 1, Position: "0:15:00"})

			media.pass()

			lines := linesAbout(log, "play house/movie")
			if len(lines) == 0 || lines[0] != each.want {
				t.Errorf("the lines about the play are %q, want the first to be %q", lines, each.want)
			}
		})
	}
}

// A newer Play on the same Player replaces the running one.
func TestASupersededPlayLogsItsDelete(t *testing.T) {
	cluster := runningCluster(housePlayer())
	cluster.plays["movie"].Metadata.CreationTimestamp = "2026-01-01T00:00:00Z"
	newer := housePlay("https://nas/next.mkv")
	newer.Metadata.Name = "sequel"
	newer.Metadata.CreationTimestamp = "2026-01-02T00:00:00Z"
	cluster.plays["sequel"] = newer
	media, log := loggingOperator(t, cluster)

	media.pass()

	mustMatchAll(t, linesAbout(log, "play house/movie"), []string{
		"play house/movie: deleted, because a newer play on player theater replaces it",
	})
}

func TestADeletedPlayLogsItsRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		play := housePlay("https://nas/film.mkv")
		play.Metadata.DeletionTimestamp = "2026-01-01T00:00:00Z"
		play.Metadata.Finalizers = []string{playFinalizer}
		cluster.plays["movie"] = play
		media, _ := busOperator(t, cluster)
		var log logBuffer
		media.log = &log

		media.pass()

		mustMatchAll(t, linesAbout(&log, "play house/movie"), []string{
			"play house/movie: deleted, so its playback pod is gone and its topics are cleared",
		})
	})
}

func TestAFocusMoveLogsItsReason(t *testing.T) {
	topic := remoteFocusTopic(defaultTopicBase, "house", "sofa")
	key := controllerKey("house", "sofa")
	cases := []struct {
		name    string
		mark    string
		cycle   bool
		players []Player
		want    []string
	}{
		{
			name:    "a cycle to the next player",
			mark:    "aaa",
			cycle:   true,
			players: []Player{focusPlayer("aaa", "sofa"), focusPlayer("bbb", "sofa")},
			want:    []string{"remote house/sofa: focus moved from player aaa to player bbb, because the remote asked to cycle focus, published to " + topic},
		},
		{
			name:    "a cycle with one player",
			mark:    "aaa",
			cycle:   true,
			players: []Player{focusPlayer("aaa", "sofa")},
			want:    []string{"remote house/sofa: asked to cycle focus, focus stays on player aaa, the only Player that lists the Remote"},
		},
		{
			name:    "a cycle with no player",
			cycle:   true,
			players: []Player{focusPlayer("aaa")},
			want:    []string{"remote house/sofa: asked to cycle focus, ignored, because no player lists the remote"},
		},
		{
			name:    "a mark on a player that dropped the remote",
			mark:    "console",
			players: []Player{focusPlayer("aaa", "sofa")},
			want:    []string{"remote house/sofa: focus moved from player console to player aaa, because the mark named no player that lists the remote, published to " + topic},
		},
		{
			name:    "no mark yet",
			players: []Player{focusPlayer("aaa", "sofa")},
			want:    []string{"remote house/sofa: focus set on player aaa, because the mark named no player that lists the remote, published to " + topic},
		},
		{
			name:    "a remote no player lists",
			mark:    "aaa",
			players: []Player{focusPlayer("aaa")},
			want:    []string{"remote house/sofa: focus cleared from player aaa, because no player lists the remote, published the empty mark to " + topic},
		},
		{
			name:    "a mark that stays",
			mark:    "bbb",
			players: []Player{focusPlayer("aaa", "sofa"), focusPlayer("bbb", "sofa")},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o, _ := caughtUpFocusOperator(t)
				var log logBuffer
				o.log = &log
				if each.mark != "" {
					o.focus.setMark(key, each.mark)
				}
				if each.cycle {
					o.focus.requestCycle(key)
				}

				o.reconcileFocus(each.players)

				mustMatchAll(t, log.lines(), each.want)
			})
		})
	}
}

// A new Play moves the focus of its Player's controllers to that Player.
func TestAFreshPlayLogsTheFocusItTakes(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media, log := loggingOperator(t, cluster)
	media.focus.setMark(controllerKey("house", "sofa"), "console")

	media.pass()

	mustMatch(t, linesAbout(log, "remote house/sofa")[0],
		"remote house/sofa: focus moved from player console to player theater, because play house/movie started there, published to "+
			remoteFocusTopic(defaultTopicBase, "house", "sofa"))
}

// A key press on a controller whose Player is wired to a Receiver makes
// the operator write an inputAsk on the Receiver. A repeat logs nothing.
func TestAPressLogsTheInputAskItSent(t *testing.T) {
	media, _ := askOperator(t)
	var log logBuffer
	media.log = &log

	press(media, "KEY_UP", 1)
	press(media, "KEY_UP", 2)

	mustLogOnce(t, &log, "remote house/sofa: KEY_UP pressed with focus on player theater, wrote inputAsk ensure on receiver den-receiver")
}

// Publishing the same key table again after a broker reconnect logs
// nothing.
func TestAKeyTableLogsOnceUntilItChanges(t *testing.T) {
	media := testOperator(t, newFakeCluster(), make(chan struct{}, 1))
	media.bus = connectedBus(t)
	var log logBuffer
	media.log = &log
	keymaps := map[string]*Keymap{"gamepad": testKeymap()}
	topic := remoteKeysTopic(defaultTopicBase, "house", "sofa")

	table := media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})
	media.reestablishRetained()
	media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})

	mustLogOnce(t, &log, "remote house/sofa: published a key table of "+countOf(len(table), "row", "rows")+
		", the base with Keymap gamepad, to "+topic)
}

// A Keymap that does not compile logs the compile error once, however
// many times the operator publishes the key table.
func TestARefusedKeymapLogsOnce(t *testing.T) {
	media := testOperator(t, newFakeCluster(), make(chan struct{}, 1))
	var log logBuffer
	media.log = &log
	broken := testKeymap()
	broken.Spec.Buttons = []KeymapButton{{Press: "BTN_NOPE", Key: "KEY_UP"}}
	keymaps := map[string]*Keymap{"gamepad": broken}

	media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})
	media.publishKeys(houseRemote("gamepad"), keymaps, map[string]bool{})

	mustLogOnce(t, &log, "remote house/sofa: key table not published, the last good table stays: compiling the base with Keymap gamepad: ",
		"BTN_NOPE")
}

func TestAnActivityChangeLogsOnce(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media, log := loggingOperator(t, cluster)
	player := housePlayer()
	playing := []Play{*cluster.plays["movie"]}

	media.publishPlayerStatus(player, nil)
	media.publishPlayerStatus(player, playing)
	media.publishPlayerStatus(player, playing)

	mustLogOnce(t, log, "player house/theater: activity "+playerPlaying+", was "+playerIdle+", play movie, published to "+
		playerStatusTopic(defaultTopicBase, "house", "theater"))
}

// The first read of a controller's link logs nothing.
func TestALinkChangeLogsOnce(t *testing.T) {
	media, log := loggingOperator(t, newFakeCluster())
	named := map[string]string{"house/sofa": "aa-bb"}
	linked := func(status string) []Peripheral {
		return []Peripheral{{
			Metadata: ObjectMeta{Name: "aa-bb"},
			Status:   PeripheralStatus{Conditions: []PeripheralCondition{{Type: peripheralConnected, Status: status}}},
		}}
	}

	for _, status := range []string{conditionTrue, conditionTrue, "False", "False", conditionTrue} {
		before := media.peripherals.links()
		media.peripherals.replace(linked(status), named)
		media.logLinks(before, media.peripherals.links())
	}

	mustMatchAll(t, log.lines(), []string{
		"remote house/sofa: controller disconnected, Peripheral aa-bb reports Connected False",
		"remote house/sofa: controller connected, Peripheral aa-bb reports Connected True",
	})
}

// A Remote's pod whose template changed is deleted on one pass and
// created on the next, and each step logs one line.
func TestARemotePodReplacedByAnEditLogsEachStep(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)
	seedPersistent(t, cluster, claim,
		buildRemotePod(remote, claim, "registry.example/sidecar:old", media.busAddress, media.topicBase))

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	mustMatchAll(t, log.lines(), []string{
		"remote house/sofa: deleted pod sofa-remote, because its template changed with a spec edit or a new release",
		"remote house/sofa: created pod sofa-remote",
	})
}

// A claim whose template changed is replaced together with its pods.
func TestARemoteClaimReplacedByAnEditLogsBoth(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	remote := remoteWithController()
	claim := buildRemoteClaim(remote)
	seedPersistent(t, cluster, claim,
		buildRemotePod(remote, claim, media.sidecarImage, media.busAddress, media.topicBase))
	remote.Spec.Device.Class = "another-class"

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	mustMatchAll(t, log.lines(), []string{
		"remote house/sofa: deleted claim sofa-remote-devices and the pods that hold it, because its template changed with a spec edit or a new release",
		"remote house/sofa: created claim sofa-remote-devices",
		"remote house/sofa: created pod sofa-remote",
	})
}

// Handing the idle screen to another controller deletes this operator's
// idle pod.
func TestAnIdleScreenHandedToADelegateLogsTheHandOver(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	seedPersistent(t, cluster, claim, plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York"))

	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	mustMatchAll(t, log.lines(), []string{
		"player house/theater: deleted pod theater-idle, because the idle screen went to controller library.liken.sh/media-browser",
	})
}

// A pass that applies a receiver session that is already applied logs
// nothing.
func TestAReceiverSessionLogsItsApplyAndItsLift(t *testing.T) {
	cluster := receiverCluster()
	media, log := loggingOperator(t, cluster)

	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	runPlayers(media, nil, nil)

	mustMatchAll(t, linesAbout(log, "player house/theater"), []string{
		"player house/theater: applied the session on receiver den-receiver: input GAME, active true, awake true",
		"player house/theater: removed the session from Receiver den-receiver, because the Player matches no Receiver input now",
	})
}

func TestAUnitThatMovesReceiverLogsTheLiftAndTheApply(t *testing.T) {
	cluster := receiverCluster()
	cluster.receivers["den-denon"] = &Receiver{
		Metadata: ObjectMeta{Name: "den-denon"},
		Spec:     ReceiverSpec{Inputs: []ReceiverInput{{Name: "MPLAY", Machine: "node-6", Monitor: testMonitor}}},
	}
	media, log := loggingOperator(t, cluster)
	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	cluster.receivers["den-receiver"].Spec.Inputs[1].Machine = "node-6"
	cluster.receivers["den-denon"].Spec.Inputs[0].Machine = testNode

	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatchAll(t, linesAbout(log, "player house/theater")[1:], []string{
		"player house/theater: removed the session from Receiver den-receiver, because the Player moved to Receiver den-denon",
		"player house/theater: applied the session on receiver den-denon: input MPLAY, active true, awake true",
	})
}

// A change in the panel state that the idle screen asks for logs once.
func TestAPanelDesireLogsTheOverrideOnce(t *testing.T) {
	cluster := screenCluster()
	media, log := loggingOperator(t, cluster)
	players := []Player{*housePlayer()}

	sendPanelDesireAndReconcile(media, players, panelDesireOff, nil)
	sendPanelDesireAndReconcile(media, players, panelDesireOff, nil)
	sendPanelDesireAndReconcile(media, players, "on", nil)

	mustMatchAll(t, linesAbout(log, "player house/theater"), []string{
		"player house/theater: the idle screen requested panel off, applied backlight Off to Display " + testMonitor,
		"player house/theater: the idle screen requested panel on, applied no override to Display " + testMonitor,
	})
}

func TestDescribeOverrideNamesTheBlock(t *testing.T) {
	mustMatch(t, describeOverride(&DisplayOverride{Power: displayPowerOff}), "power Off")
}

// When a Player is gone, the operator removes the panel override that it
// set for the Player's idle screen, and logs it.
func TestAGoneUnitLogsTheLiftOfItsOverride(t *testing.T) {
	cluster := screenCluster()
	media, log := loggingOperator(t, cluster)
	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	media.removeGonePanelOverrides(map[string]bool{})

	mustMatch(t, linesAbout(log, "player house/theater")[1],
		"player house/theater: removed the override from Display "+testMonitor+", because the Player was deleted")
}
