package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// focusOperator builds an operator whose bus never starts, so
// publishFocus stores the mark in o.focus and sends nothing.
func focusOperator(t *testing.T) *operator {
	t.Helper()
	media := &operator{
		topicBase:       defaultTopicBase,
		bus:             newBus("bus.media.svc:1883", "focus-test", nil, nil, nil),
		focus:           newFocusCache(make(chan struct{}, 1)),
		playerReceivers: newPlayerReceivers(),
		log:             io.Discard,
	}
	media.levels = media.newVolumeEngine()
	return media
}

// focusBrokerOperator connects the operator's bus to a fake broker, so a
// test can read the retained messages that publishFocus sends.
func focusBrokerOperator(t *testing.T) (*operator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	media := &operator{
		topicBase:       defaultTopicBase,
		bus:             bus,
		focus:           newFocusCache(make(chan struct{}, 1)),
		playerReceivers: newPlayerReceivers(),
		log:             io.Discard,
	}
	media.levels = media.newVolumeEngine()
	return media, brokers[0]
}

// focusPlay returns a Play on one Player in the house namespace.
func focusPlay(name, player, phase string) Play {
	return Play{
		Metadata: ObjectMeta{Name: name, Namespace: "house"},
		Spec:     PlaySpec{Players: []string{player}},
		Status:   PlayStatus{Phase: phase},
	}
}

// focusPlayer returns a Player in the house namespace whose
// spec.remotes lists the given Remotes.
func focusPlayer(name string, remotes ...string) Player {
	entries := make([]PlayerRemote, 0, len(remotes))
	for _, remote := range remotes {
		entries = append(entries, PlayerRemote{Name: remote})
	}
	return Player{Metadata: ObjectMeta{Name: name, Namespace: "house"}, Spec: PlayerSpec{Remotes: entries}}
}

// boundSofa returns the sofa Remote as the pod builders receive it.
func boundSofa() []boundRemote {
	return []boundRemote{{
		Name:        "sofa",
		EventsTopic: remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		FocusTopic:  remoteFocusTopic(defaultTopicBase, "house", "sofa"),
	}}
}

// remotePlay returns a Play with one item on the named Player.
func remotePlay(name, player string) *Play {
	return &Play{
		Metadata: ObjectMeta{Name: name, Namespace: "house", UID: name + "-uid", ResourceVersion: "9"},
		Spec:     PlaySpec{Players: []string{player}, Items: []PlayItem{{URI: "https://nas/film.mkv"}}},
	}
}

// remotePlayer returns housePlayer with the given name and with the sofa
// Remote in its spec.remotes.
func remotePlayer(name string) *Player {
	player := housePlayer()
	player.Metadata.Name = name
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}}
	return player
}

func TestANewPlaySetsTheFocusMarkToItsPlayer(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := media.focus.markFor(controllerKey("house", "sofa")); got != "theater" {
		t.Errorf("focus mark = %q, want the new Play's Player", got)
	}
}

// A Play whose pod the operator recreates is not a new Play, so it does
// not take the mark from the Player that has it.
func TestARecreatedPlayDoesNotTakeTheFocusMark(t *testing.T) {
	cluster := newFakeCluster()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()

	// game is running and unchanged, on the Player that has the mark.
	game := remotePlay("game", "console")
	game.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "game-playback"}
	console := remotePlayer("console")
	cluster.plays["game"] = game
	cluster.players["console"] = console
	gamePod := buildPod(game, buildClaim(game, console),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "registry.example/display:test",
		"bus.media.svc:1883", defaultTopicBase, boundSofa(), resolvedPreferences{}, nil, "")
	gamePod.Status.Phase = podRunning
	cluster.pods["game-playback"] = gamePod
	cluster.claims["game-devices"] = buildClaim(game, console)

	// movie changes its Player's display, so its claim differs from the
	// one it was built with and the operator recreates its pod.
	movie := remotePlay("movie", "theater")
	movie.Status = PlayStatus{Phase: phaseRunning, Activity: activityPlaying, Pod: "movie-playback"}
	theater := remotePlayer("theater")
	moviePod := buildPod(movie, buildClaim(movie, theater),
		resolution{Items: []string{"https://nas/film.mkv"}},
		"registry.example/player:test", "registry.example/sidecar:test", "registry.example/display:test",
		"bus.media.svc:1883", defaultTopicBase, boundSofa(), resolvedPreferences{}, nil, "")
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
		t.Errorf("focus mark = %q, want console; a recreated Play must not take the mark", got)
	}
	if _, held := cluster.pods["movie-playback"]; !held {
		t.Fatalf("the requests are %v, want the movie pod still present", cluster.requests)
	}
	if got := countMethod(cluster.requests, "DELETE"); got == 0 {
		t.Errorf("the requests are %v, want a delete to recreate the reshaped Play", cluster.requests)
	}
}

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

// The cycle set comes from the Players alone, so bbb is in it with no
// Play.
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

// The repeated mark is the feedback for the press, so the operator
// sends it although the value did not change.
func TestACycleWithOneBoundPlayerRepublishesTheSameMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

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

// The mark names a Player, so it stays on the Player when its Play
// finishes.
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

// When no Player lists a Remote, its mark is cleared, so no reader
// accepts presses for a Player that dropped the Remote.
func TestReconcileFocusClearsAMarkWithNoBoundPlayer(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	o.focus.setMark(key, "theater")

	o.reconcileFocus([]Player{focusPlayer("theater")})

	if got := o.focus.markFor(key); got != "" {
		t.Errorf("mark = %q, want it cleared", got)
	}
}

// caughtUpFocusOperator is focusBrokerOperator with the catch-up time
// over, so an empty focusCache means that the broker retains no mark.
func caughtUpFocusOperator(t *testing.T) (*operator, *fakeBroker) {
	t.Helper()
	o, broker := focusBrokerOperator(t)
	o.catchUpEnds = time.Now()
	return o, broker
}

func TestReconcileFocusSetsAMarkTheBrokerDoesNotHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, broker := caughtUpFocusOperator(t)

		o.reconcileFocus([]Player{focusPlayer("theater", "sofa")})

		published := waitForPublish(t, broker.pubs)
		mustMatch(t, published.topic, remoteFocusTopic(defaultTopicBase, "house", "sofa"))
		mustMatch(t, string(published.payload), "theater")
	})
}

// Before the catch-up time ends, the operator publishes no mark for an
// empty focusCache, so a retained mark that arrives late is not replaced
// with the first Player of the set.
func TestReconcileFocusWritesNoMarkBeforeTheCatchUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, broker := focusBrokerOperator(t)
		players := []Player{focusPlayer("aaa", "sofa"), focusPlayer("theater", "sofa")}

		o.reconcileFocus(players)
		o.reestablishRetained()
		o.reconcileFocus(players)
		o.handleBusMessage(remoteFocusTopic(defaultTopicBase, "house", "sofa"), []byte("theater"))
		o.catchUpEnds = time.Now()
		o.reconcileFocus(players)

		mustMatch(t, o.focus.markFor(controllerKey("house", "sofa")), "theater")
		mustPublishNothing(t, broker)
	})
}

// The focus indicator does not wait for a second pass.
func TestOnePassMarksTheFocusedRemoteOnTheBusStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		cluster.players["theater"] = housePlayerWithRemote()
		cluster.remotes["sofa"] = houseRemote("gamepad")
		cluster.keymaps["gamepad"] = testKeymap()
		media, _ := caughtUpOperator(t, cluster)

		media.pass()

		topic := playerStatusTopic(defaultTopicBase, "house", "theater")
		mustMatch(t, strings.Contains(media.playerStatuses.payloadFor(topic), `"focused":true`), true)
	})
}

func TestSetMarkWakesTheLoopOnAChange(t *testing.T) {
	wake := make(chan struct{}, 1)
	cache := newFocusCache(wake)

	cache.setMark(controllerKey("house", "sofa"), "theater")
	select {
	case <-wake:
	default:
		t.Fatal("the loop did not wake after the mark changed, want it woken")
	}

	cache.setMark(controllerKey("house", "sofa"), "theater")
	select {
	case <-wake:
		t.Error("the loop woke after an unchanged mark, want it asleep")
	default:
	}
}

// When a new broker session sends no mark for a controller, the broker
// lost it. Once the catch-up time ends, the operator publishes the mark
// it stored, once, so the controller keeps its Player.
func TestANewSessionRestoresAMarkTheBrokerLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, broker := focusBrokerOperator(t)
		o.focus.setMark(controllerKey("house", "sofa"), "theater")
		players := []Player{focusPlayer("aaa", "sofa"), focusPlayer("theater", "sofa")}

		o.focus.newSession()
		o.reestablishRetained()
		o.reconcileFocus(players)
		mustPublishNothing(t, broker)
		o.catchUpEnds = time.Now()
		o.reconcileFocus(players)
		o.reconcileFocus(players)

		published := waitForPublish(t, broker.pubs)
		mustMatch(t, published.topic, remoteFocusTopic(defaultTopicBase, "house", "sofa"))
		mustMatch(t, string(published.payload), "theater")
		mustMatch(t, published.retained, true)
		mustPublishNothing(t, broker)
	})
}

// A mark that the broker sends on the new session is still retained, so
// the operator publishes nothing. Each publish reaches every reader as a
// live mark that no person caused.
func TestANewSessionDoesNotRepublishAMarkTheBrokerHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, broker := focusBrokerOperator(t)
		o.focus.setMark(controllerKey("house", "sofa"), "theater")
		players := []Player{focusPlayer("aaa", "sofa"), focusPlayer("theater", "sofa")}

		o.focus.newSession()
		o.reestablishRetained()
		o.handleBusMessage(remoteFocusTopic(defaultTopicBase, "house", "sofa"), []byte("theater"))
		o.catchUpEnds = time.Now()
		o.reconcileFocus(players)

		mustPublishNothing(t, broker)
	})
}
