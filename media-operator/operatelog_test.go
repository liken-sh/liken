package main

// These tests cover the operator's lines: one line per operation a
// person caused, and none for a pass that finds everything as it wants
// it.

import (
	"strings"
	"testing"
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
// reads past the lines the same pass writes about its Remotes.
func linesAbout(log *logBuffer, subject string) []string {
	var kept []string
	for _, line := range log.lines() {
		if strings.HasPrefix(line, subject+":") {
			kept = append(kept, line)
		}
	}
	return kept
}

// A new Play is two lines, the pod and the phase, and a second pass over
// the same Play writes none.
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

// A Play the operator cannot run says why, in the words its status
// carries, once.
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
				t.Errorf("lines = %q, want the first to be %q", lines, each.want)
			}
		})
	}
}

// A newer Play on the same Player replaces the running one, and the
// line names the unit it ran on.
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

// A Play a person deleted is released once its pod is gone, and the
// release is its one line.
func TestADeletedPlayLogsItsRelease(t *testing.T) {
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
}

// A Play's spec.volume is a level a person asked for, so its write says
// where it went.
func TestASpecVolumeLogsTheLevelItSet(t *testing.T) {
	cluster := newFakeCluster()
	play := housePlay("https://nas/film.mkv")
	play.Spec.Volume = &PlayVolume{Level: level(35)}
	cluster.plays["movie"] = play
	cluster.players["theater"] = housePlayer()
	media, _ := caughtUpOperator(t, cluster)
	var log logBuffer
	media.log = &log

	media.pass()

	lines := linesAbout(&log, "play house/movie")
	mustMatch(t, lines[0], "play house/movie: spec.volume set player theater to level 35, not muted, published to "+theaterVolumeTopic())
}

// A moving mark writes one line with its reason, and a mark that stands
// writes none.
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
			want:    []string{"remote house/sofa: asked to cycle focus, focus stays on player aaa, the one player that lists the remote"},
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
			name:    "a mark that stands",
			mark:    "bbb",
			players: []Player{focusPlayer("aaa", "sofa"), focusPlayer("bbb", "sofa")},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
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
	}
}

// A fresh Play takes its controllers, and the line names the Play.
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

// A press on a controller whose unit has a receiver asks it for the
// unit's input, and the line names the press and the ask.
func TestAPressLogsTheInputAskItSent(t *testing.T) {
	media, broker := focusBrokerOperator(t)
	var log logBuffer
	media.log = &log
	media.focus.setMark(controllerKey("media", "den-remote"), "den")
	media.ensure.set(playerKey("media", "den"), "liken/equipment/den-receiver/commands")

	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "media", "den-remote"), []byte(`{"key":"KEY_UP","value":1}`))
	waitForPublish(t, broker.pubs)
	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "media", "den-remote"), []byte(`{"key":"KEY_UP","value":2}`))

	mustLogOnce(t, &log, "remote media/den-remote: KEY_UP pressed with focus on player den, published input.ensure to liken/equipment/den-receiver/commands")
}

// A new key table writes one line, and the same table republished after
// a fresh broker session writes none.
func TestAKeyTableLogsOnceUntilItChanges(t *testing.T) {
	media := testOperator(t, newFakeCluster(), make(chan struct{}, 1))
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

// A Keymap that does not compile is refused in the compiler's words, once
// for as long as it stands.
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

// A unit that starts or ends a run writes one line, and a republish of
// the same activity writes none.
func TestAnActivityMoveLogsOnce(t *testing.T) {
	cluster := runningCluster(housePlayer())
	media, log := loggingOperator(t, cluster)
	player := housePlayer()
	playing := []Play{*cluster.plays["movie"]}

	media.publishPlayerStatus(player, PlayerStatus{Activity: playerIdle}, nil)
	media.publishPlayerStatus(player, derivePlayerStatus(player, playing, media.reports), playing)
	media.publishPlayerStatus(player, derivePlayerStatus(player, playing, media.reports), playing)

	mustLogOnce(t, log, "player house/theater: activity "+playerPlaying+", was "+playerIdle+", play movie, published to "+
		playerStatusTopic(defaultTopicBase, "house", "theater"))
}

// A controller whose link changes between passes writes one line, and
// the first read of a link writes none.
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
		media.peripherals.hold(linked(status), named)
		media.logLinks(before, media.peripherals.links())
	}

	mustMatchAll(t, log.lines(), []string{
		"remote house/sofa: controller disconnected, Peripheral aa-bb reports Connected False",
		"remote house/sofa: controller connected, Peripheral aa-bb reports Connected True",
	})
}

// A standing pod whose template changed is replaced over two passes,
// and each step writes its line with the reason.
func TestAStandingPodReplacedByAnEditLogsEachStep(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	seedStanding(t, cluster, claim,
		buildRemotePod(remote, claim, "registry.example/sidecar:old", media.busAddress, media.topicBase))

	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))
	mustSucceed(t, media.reconcileRemote(remote, claimRead{}))

	mustMatchAll(t, log.lines(), []string{
		"remote house/sofa: deleted pod sofa-remote, because its template changed with a spec edit or a new release",
		"remote house/sofa: created pod sofa-remote",
	})
}

// A claim that diverged takes its pods with it, and the line says so.
func TestAStandingClaimReplacedByAnEditLogsBoth(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	remote := standingRemote()
	claim := buildRemoteClaim(remote)
	seedStanding(t, cluster, claim,
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

// Handing the idle screen to a delegate removes this operator's pod, and
// the line names the controller that took it.
func TestAnIdleScreenHandedToADelegateLogsTheHandOver(t *testing.T) {
	cluster := newFakeCluster()
	media, log := loggingOperator(t, cluster)
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, media.idleDisplayClass)
	seedStanding(t, cluster, claim, plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York"))

	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	mustMatchAll(t, log.lines(), []string{
		"player house/theater: deleted pod theater-idle, because the idle screen went to controller library.liken.sh/media-browser",
	})
}

// A session applied and a session lifted each write one line, and a pass
// that applies the session it already holds writes none.
func TestAReceiverSessionLogsItsApplyAndItsLift(t *testing.T) {
	cluster := receiverCluster()
	media, log := loggingOperator(t, cluster)

	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	runPlayers(media, nil, nil)

	mustMatchAll(t, linesAbout(log, "player house/theater"), []string{
		"player house/theater: applied the session on receiver living-room-denon: input GAME, active true, awake true",
		"player house/theater: lifted the session on receiver living-room-denon, because the unit matches no receiver input now",
	})
}

func TestAUnitThatMovesReceiverLogsTheLiftAndTheApply(t *testing.T) {
	cluster := receiverCluster()
	cluster.receivers["den-denon"] = &Receiver{
		Metadata: ObjectMeta{Name: "den-denon"},
		Spec:     ReceiverSpec{Inputs: []ReceiverInput{{Name: "MPLAY", Machine: "nuc6", Monitor: testMonitor}}},
	}
	media, log := loggingOperator(t, cluster)
	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	cluster.receivers["living-room-denon"].Spec.Inputs[1].Machine = "nuc6"
	cluster.receivers["den-denon"].Spec.Inputs[0].Machine = testNode

	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	mustMatchAll(t, linesAbout(log, "player house/theater")[1:], []string{
		"player house/theater: lifted the session on receiver living-room-denon, because the unit moved to receiver den-denon",
		"player house/theater: applied the session on receiver den-denon: input MPLAY, active true, awake true",
	})
}

// The idle screen's desire reaches the panel once per change, and the
// line names the override and the display.
func TestAPanelDesireLogsTheOverrideOnce(t *testing.T) {
	cluster := screenCluster()
	media, log := loggingOperator(t, cluster)
	players := []Player{*housePlayer()}

	statePanel(media, players, panelDesireOff, nil)
	statePanel(media, players, panelDesireOff, nil)
	statePanel(media, players, "on", nil)

	mustMatchAll(t, linesAbout(log, "player house/theater"), []string{
		"player house/theater: the idle screen asked for panel off, applied backlight off to display " + testMonitor,
		"player house/theater: the idle screen asked for panel on, applied no override to display " + testMonitor,
	})
}

func TestDescribeOverrideNamesTheBlock(t *testing.T) {
	mustMatch(t, describeOverride(&DisplayOverride{Power: displayPowerOff}), "power off")
}

// A unit that is gone lifts the override it left, and the line says so.
func TestAGoneUnitLogsTheLiftOfItsOverride(t *testing.T) {
	cluster := screenCluster()
	media, log := loggingOperator(t, cluster)
	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	media.retainPanels(map[string]bool{})

	mustMatch(t, linesAbout(log, "player house/theater")[1],
		"player house/theater: lifted the override on display "+testMonitor+", because the player is gone")
}
