package main

// These tests cover focus arbitration against Players. A fresh Play
// marks its Player, a graceful recreate marks nothing, a cycle advances the
// mark through the Players that name the Remote in name order and wraps, a
// one-Player cycle republishes the same mark, an idle Player is in the cycle
// set, a mark on a Player that no longer names the Remote recovers to the
// first of the set, and a Remote no Player names has its mark cleared.

import (
	"encoding/json"
	"strings"
	"testing"
)

// focusOperator builds an operator with a focus desk and a bus that is
// never Run, so publishFocus drops on the wire and records on the desk.
func focusOperator(t *testing.T) *operator {
	t.Helper()
	return &operator{
		topicBase: defaultTopicBase,
		bus:       newBus("bus.media.svc:1883", "focus-test", nil, nil, nil),
		focus:     newFocusDesk(make(chan struct{}, 1)),
		volumes:   newVolumeDesk(),
	}
}

// focusBrokerOperator wires the focus desk to a fake broker, so a test
// reads the retained messages publishFocus writes and not only the desk.
func focusBrokerOperator(t *testing.T) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &operator{
		topicBase: defaultTopicBase,
		bus:       bus,
		focus:     newFocusDesk(make(chan struct{}, 1)),
		volumes:   newVolumeDesk(),
	}, brokers[0]
}

// focusPlay is one Play on one Player in the house namespace.
func focusPlay(name, player, phase string) Play {
	return Play{
		Metadata: ObjectMeta{Name: name, Namespace: "house"},
		Spec:     PlaySpec{Players: []string{player}},
		Status:   PlayStatus{Phase: phase},
	}
}

// focusPlayer is one Player naming the given controllers in the house
// namespace.
func focusPlayer(name string, remotes ...string) Player {
	entries := make([]PlayerRemote, 0, len(remotes))
	for _, remote := range remotes {
		entries = append(entries, PlayerRemote{Name: remote})
	}
	return Player{Metadata: ObjectMeta{Name: name, Namespace: "house"}, Spec: PlayerSpec{Remotes: entries}}
}

// boundSofa is the house sofa controller as the pod builders carry it.
func boundSofa() []boundRemote {
	return []boundRemote{{
		Name:        "sofa",
		Keymap:      "gamepad",
		EventsTopic: remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		KeymapTopic: keymapTopic(defaultTopicBase, "gamepad"),
		FocusTopic:  remoteFocusTopic(defaultTopicBase, "house", "sofa"),
	}}
}

// remotePlay is a Play on a named Player, with a resolvable URI.
func remotePlay(name, player string) *Play {
	return &Play{
		Metadata: ObjectMeta{Name: name, Namespace: "house", UID: name + "-uid", ResourceVersion: "9"},
		Spec:     PlaySpec{Players: []string{player}, Items: []PlayItem{{URI: "https://nas/film.mkv"}}},
	}
}

// remotePlayer is the house player renamed and set to own the sofa
// controller.
func remotePlayer(name string) *Player {
	player := housePlayer()
	player.Metadata.Name = name
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}}
	return player
}

// A fresh Play marks its Player, so the controller drives the unit the
// newest film runs on.
func TestAFreshPlayStealsTheFocusMarkForItsPlayer(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := media.focus.markFor(controllerKey("house", "sofa")); got != "theater" {
		t.Errorf("focus mark = %q, want the fresh play's Player", got)
	}
}

// A graceful recreate resumes the same Play and steals nothing, so a mark
// another live Play holds stays where it was.
func TestAGracefulRecreateDoesNotStealTheFocusMark(t *testing.T) {
	cluster := newFakeCluster()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()

	// game holds the mark and keeps running unchanged.
	game := remotePlay("game", "console")
	game.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "game-playback"}
	console := remotePlayer("console")
	cluster.plays["game"] = game
	cluster.players["console"] = console
	gamePod := buildPod(game, buildClaim(game, console),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "bus.media.svc:1883", defaultTopicBase, boundSofa(), resolvedPreferences{})
	gamePod.Status.Phase = podRunning
	cluster.pods["game-playback"] = gamePod
	cluster.claims["game-devices"] = buildClaim(game, console)

	// movie is reshaped so its claim diverges and its pod recreates.
	movie := remotePlay("movie", "theater")
	movie.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback"}
	theater := remotePlayer("theater")
	moviePod := buildPod(movie, buildClaim(movie, theater),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "bus.media.svc:1883", defaultTopicBase, boundSofa(), resolvedPreferences{})
	moviePod.Status.Phase = podRunning
	cluster.pods["movie-playback"] = moviePod
	cluster.claims["movie-devices"] = buildClaim(movie, theater)
	theater.Spec.Display = &PlayerDevice{
		Class:      "display-output",
		Parameters: &DeviceParameters{Driver: "display.liken.sh", Values: json.RawMessage(`{"brightness":80}`)},
	}
	cluster.plays["movie"] = movie
	cluster.players["theater"] = theater

	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.focus.setMark(controllerKey("house", "sofa"), "console")

	media.pass()

	if got := media.focus.markFor(controllerKey("house", "sofa")); got != "console" {
		t.Errorf("focus mark = %q, want console; a graceful recreate must not steal", got)
	}
	if _, held := cluster.pods["movie-playback"]; !held {
		t.Fatalf("the movie pod is gone: %v", cluster.requests)
	}
	if got := countMethod(cluster.requests, "DELETE"); got == 0 {
		t.Errorf("the reshaped play did not recreate: no delete in %v", cluster.requests)
	}
}

// A cycle advances the mark to the next Player that names the Remote,
// in name order, and wraps from the last back to the first.
func TestReconcileFocusCyclesThroughTheBoundPlayers(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{
		focusPlayer("aaa", "sofa"),
		focusPlayer("bbb", "sofa"),
	}
	o.focus.setMark(key, "aaa")

	o.focus.requestCycle(key)
	o.reconcileFocus(players)
	if got := o.focus.markFor(key); got != "bbb" {
		t.Errorf("mark = %q, want bbb after one cycle", got)
	}

	o.focus.requestCycle(key)
	o.reconcileFocus(players)
	if got := o.focus.markFor(key); got != "aaa" {
		t.Errorf("mark = %q, want aaa after the cycle wraps", got)
	}
}

// An idle Player is in the cycle set, because the set reads the
// Players that name the Remote and reads no Play at all.
func TestReconcileFocusCyclesToAnIdlePlayer(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{
		focusPlayer("aaa", "sofa"),
		focusPlayer("bbb", "sofa"),
	}
	o.focus.setMark(key, "aaa")

	o.focus.requestCycle(key)
	o.reconcileFocus(players)

	if got := o.focus.markFor(key); got != "bbb" {
		t.Errorf("mark = %q, want the idle player bbb", got)
	}
}

// A cycle on a Remote one Player names republishes the same mark. The
// retained message is the press's feedback, so it goes out whether or not the
// value changed.
func TestACycleWithOneBoundPlayerRepublishesTheSameMark(t *testing.T) {
	o, broker := focusBrokerOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{focusPlayer("theater", "sofa")}
	o.focus.setMark(key, "theater")

	o.focus.requestCycle(key)
	o.reconcileFocus(players)

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, remoteFocusTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(published.payload), "theater")
	mustMatch(t, published.retained, true)
}

// A mark on a Player that no longer names the Remote moves to the
// first of the cycle set, so a controller always drives a unit that lists it.
func TestReconcileFocusRecoversAMarkOffTheCycleSet(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{focusPlayer("theater", "sofa")}
	o.focus.setMark(key, "console")

	o.reconcileFocus(players)

	if got := o.focus.markFor(key); got != "theater" {
		t.Errorf("mark = %q, want the mark recovered to theater", got)
	}
}

// A Play that finished moves no mark, because the mark names a Player
// and a unit whose film ended still holds its controllers.
func TestReconcileFocusLeavesTheMarkWhenAPlayFinishes(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{
		focusPlayer("aaa", "sofa"),
		focusPlayer("bbb", "sofa"),
	}
	o.focus.setMark(key, "bbb")

	o.reconcileFocus(players)

	if got := o.focus.markFor(key); got != "bbb" {
		t.Errorf("mark = %q, want bbb; a finished film moves no mark", got)
	}
}

// A Remote no Player names has an empty cycle set, so its mark is
// cleared and no translator gates on a unit that dropped it.
func TestReconcileFocusClearsAMarkWithNoBoundPlayer(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	o.focus.setMark(key, "theater")

	o.reconcileFocus([]Player{focusPlayer("theater")})

	if got := o.focus.markFor(key); got != "" {
		t.Errorf("mark = %q, want it cleared", got)
	}
}

// reconcileFocus over a bus that never Ran records a recovered mark on
// the desk and does not panic, the state a pass runs in under the test.
func TestReconcileFocusOverANeverRunBusIsSafe(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	players := []Player{focusPlayer("theater", "sofa")}

	o.reconcileFocus(players)

	if got := o.focus.markFor(key); got != "theater" {
		t.Errorf("mark = %q, want theater set by recovery", got)
	}
}

// One pass settles the mark and publishes it on the unit's bus
// status, so the focus indicator is never a pass behind the press.
func TestOnePassMarksTheFocusedRemoteOnTheBusStatus(t *testing.T) {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	topic := playerStatusTopic(defaultTopicBase, "house", "theater")
	mustMatch(t, strings.Contains(media.playerStatusPublished[topic], `"focused":true`), true)
}

// A stored value that changes wakes the loop, because the Player bus
// status and the Remote status both derive from the mark. A repeat of the
// same value wakes nothing.
func TestSetMarkWakesTheLoopOnAChange(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newFocusDesk(wake)

	desk.setMark(controllerKey("house", "sofa"), "theater")
	select {
	case <-wake:
	default:
		t.Fatal("a changed mark did not wake the loop")
	}

	desk.setMark(controllerKey("house", "sofa"), "theater")
	select {
	case <-wake:
		t.Error("an unchanged mark woke the loop")
	default:
	}
}
