package main

// These tests cover focus arbitration: a fresh Play steals its
// controllers, a graceful recreate does not, a cycle advances the mark in
// stable order and wraps, and a mark on a finished Play recovers to a live
// one.

import (
	"encoding/json"
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
	}
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

// A fresh Play with a controller steals that controller's mark on create,
// the most-recent-steals default.
func TestAFreshPlayStealsTheFocusMark(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = housePlayerWithRemote()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	cluster.keymaps["gamepad"] = testKeymap()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	if got := media.focus.markFor(controllerKey("house", "sofa")); got != "movie" {
		t.Errorf("focus mark = %q, want the fresh play to have stolen it", got)
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
	media.focus.setMark(controllerKey("house", "sofa"), "game")

	media.pass()

	if got := media.focus.markFor(controllerKey("house", "sofa")); got != "game" {
		t.Errorf("focus mark = %q, want game; a graceful recreate must not steal", got)
	}
	if _, held := cluster.pods["movie-playback"]; !held {
		t.Fatalf("the movie pod is gone: %v", cluster.requests)
	}
	if got := countMethod(cluster.requests, "DELETE"); got == 0 {
		t.Errorf("the reshaped play did not recreate: no delete in %v", cluster.requests)
	}
}

// A cycle request advances the mark to the next bound-and-active Play in
// name order, and wraps from the last back to the first.
func TestReconcileFocusCyclesThroughTheBoundActivePlays(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	plays := []Play{
		focusPlay("aaa", "one", phaseRunning),
		focusPlay("bbb", "two", phaseRunning),
	}
	players := []Player{
		focusPlayer("one", "sofa"),
		focusPlayer("two", "sofa"),
	}
	o.focus.setMark(key, "aaa")

	o.focus.requestCycle(key)
	o.reconcileFocus(plays, players)
	if got := o.focus.markFor(key); got != "bbb" {
		t.Errorf("mark = %q, want bbb after one cycle", got)
	}

	o.focus.requestCycle(key)
	o.reconcileFocus(plays, players)
	if got := o.focus.markFor(key); got != "aaa" {
		t.Errorf("mark = %q, want aaa after the cycle wraps", got)
	}
}

// A mark on a finished Play recovers to a live bound Play, so focus never
// rests on a Play that is gone.
func TestReconcileFocusRecoversAMarkOnAFinishedPlay(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	plays := []Play{
		focusPlay("done", "one", phaseFinished),
		focusPlay("live", "two", phaseRunning),
	}
	players := []Player{
		focusPlayer("one", "sofa"),
		focusPlayer("two", "sofa"),
	}
	o.focus.setMark(key, "done")

	o.reconcileFocus(plays, players)

	if got := o.focus.markFor(key); got != "live" {
		t.Errorf("mark = %q, want the finished play recovered to the live one", got)
	}
}

// reconcileFocus over a bus that never Ran records a recovered mark on
// the desk and does not panic, the state a pass runs in under the test.
func TestReconcileFocusOverANeverRunBusIsSafe(t *testing.T) {
	o := focusOperator(t)
	key := controllerKey("house", "sofa")
	plays := []Play{focusPlay("live", "one", phaseRunning)}
	players := []Player{focusPlayer("one", "sofa")}

	o.reconcileFocus(plays, players)

	if got := o.focus.markFor(key); got != "live" {
		t.Errorf("mark = %q, want live set by recovery", got)
	}
}
