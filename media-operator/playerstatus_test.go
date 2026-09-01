package main

// A Player's status is derived from the Plays that name it, so these
// tests hand the derivation a Player and a list of Plays and check
// the one status it earns.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func theater() *Player {
	return &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
}

// playOn builds a Play in a phase, naming one player in a namespace,
// so a case states only what it varies.
func playOn(name, namespace, player, phase string) Play {
	return Play{
		Metadata: ObjectMeta{Name: name, Namespace: namespace},
		Spec:     PlaySpec{Players: []string{player}},
		Status:   PlayStatus{Phase: phase},
	}
}

// endedDesk builds a report desk that has taken an ending report for each
// named run, through the fold the bus handler uses, so a case states the
// ending the way the sidecar publishes it.
func endedDesk(runs ...string) *reports {
	desk := newReports(nil)
	for _, run := range runs {
		namespace, name := splitRunKey(run)
		desk.fold(namespace, name, playReport{Item: 1, Ended: true})
	}
	return desk
}

func TestDerivePlayerStatus(t *testing.T) {
	cases := []struct {
		name  string
		plays []Play
		ended []string
		want  PlayerStatus
	}{
		{
			name: "no play names the player",
			want: PlayerStatus{Activity: playerIdle},
		},
		{
			name:  "a running play makes the player playing",
			plays: []Play{playOn("movie", "house", "theater", phaseRunning)},
			want:  PlayerStatus{Activity: playerPlaying, Play: "movie"},
		},
		{
			name: "a paused play still runs on the player",
			plays: []Play{{
				Metadata: ObjectMeta{Name: "movie", Namespace: "house"},
				Spec:     PlaySpec{Players: []string{"theater"}},
				Status:   PlayStatus{Phase: phaseRunning, Paused: true},
			}},
			want: PlayerStatus{Activity: playerPlaying, Play: "movie"},
		},
		{
			name:  "a pending play is starting on the player",
			plays: []Play{playOn("movie", "house", "theater", phasePending)},
			want:  PlayerStatus{Activity: playerStarting, Play: "movie"},
		},
		{
			name: "a running play wins over one still starting",
			plays: []Play{
				playOn("second", "house", "theater", phasePending),
				playOn("first", "house", "theater", phaseRunning),
			},
			want: PlayerStatus{Activity: playerPlaying, Play: "first"},
		},
		{
			name: "the earliest name breaks a tie",
			plays: []Play{
				playOn("zulu", "house", "theater", phaseRunning),
				playOn("alpha", "house", "theater", phaseRunning),
			},
			want: PlayerStatus{Activity: playerPlaying, Play: "alpha"},
		},
		{
			name:  "a finished play leaves the player idle",
			plays: []Play{playOn("movie", "house", "theater", phaseFinished)},
			want:  PlayerStatus{Activity: playerIdle},
		},
		{
			name:  "a play in another namespace is not this player's",
			plays: []Play{playOn("movie", "loft", "theater", phaseRunning)},
			want:  PlayerStatus{Activity: playerIdle},
		},
		{
			name:  "a play on a different player is ignored",
			plays: []Play{playOn("movie", "house", "gaming", phaseRunning)},
			want:  PlayerStatus{Activity: playerIdle},
		},
		{
			name:  "a running play whose sidecar reported the ending leaves the player idle",
			plays: []Play{playOn("movie", "house", "theater", phaseRunning)},
			ended: []string{runKey("house", "movie")},
			want:  PlayerStatus{Activity: playerIdle},
		},
		{
			name:  "a starting play whose sidecar reported the ending counts as neither",
			plays: []Play{playOn("movie", "house", "theater", phasePending)},
			ended: []string{runKey("house", "movie")},
			want:  PlayerStatus{Activity: playerIdle},
		},
		{
			name: "a second play still running keeps the player playing",
			plays: []Play{
				playOn("first", "house", "theater", phaseRunning),
				playOn("second", "house", "theater", phaseRunning),
			},
			ended: []string{runKey("house", "first")},
			want:  PlayerStatus{Activity: playerPlaying, Play: "second"},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := derivePlayerStatus(theater(), one.plays, endedDesk(one.ended...))
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("status = %+v, want %+v", got, one.want)
			}
		})
	}
}

// theaterWithParts is the unit the idle screen draws: a named screen, a
// named pair of speakers, and one controller, each with the friendly name
// the household set.
func theaterWithParts() *Player {
	return &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			DisplayName: "Studio Lab",
			Display:     &PlayerDevice{Class: "display-output", DisplayName: "Portable Screen"},
			Sinks:       []PlayerDevice{{Class: "audio-sink", DisplayName: "Built-in Speakers"}},
			Remotes:     []PlayerRemote{{Name: "sofa", DisplayName: "Studio Dualsense"}},
		},
	}
}

// connectedTo builds a desk that has heard one presence message, so a test
// states the fold in one line.
func connectedTo(key string, connected bool) *presenceDesk {
	desk := newPresenceDesk(nil)
	desk.setConnected(key, connected)
	return desk
}

// The bus status carries the unit's name, its activity, and its parts in
// the order the screen shows them: the display, then each sink, then each
// remote. Only the remote carries presence, and it reads the flag the desk
// folded.
func TestDerivePlayerBusStatusListsThePartsInScreenOrder(t *testing.T) {
	desk := connectedTo(controllerKey("house", "sofa"), true)
	connected := true

	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, desk, newFocusDesk(nil))

	want := playerBusStatus{
		DisplayName: "Studio Lab",
		Activity:    playerIdle,
		Components: []playerBusComponent{
			{Name: "Portable Screen", Kind: displayComponent},
			{Name: "Built-in Speakers", Kind: sinkComponent},
			{Name: "Studio Dualsense", Kind: remoteComponent, Connected: &connected},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status = %+v, want %+v", got, want)
	}
}

// A part the household named nothing falls back to its DeviceClass, and a
// remote falls back to the Remote it references, the same fallbacks the
// idle pod's environment carries.
func TestDerivePlayerBusStatusFallsBackToTheClassAndTheName(t *testing.T) {
	player := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			Display: &PlayerDevice{Class: "display-output"},
			Sinks:   []PlayerDevice{{Class: "audio-sink"}},
			Remotes: []PlayerRemote{{Name: "sofa"}},
		},
	}

	got := derivePlayerBusStatus(player, PlayerStatus{Activity: playerIdle}, nil, newPresenceDesk(nil), newFocusDesk(nil))

	mustMatch(t, got.DisplayName, "theater")
	mustMatchAll(t, componentNames(got), []string{"display-output", "audio-sink", "sofa"})
}

// focusedOn builds a focus desk holding one mark, so a case states
// which unit a controller drives in one line.
func focusedOn(key, player string) *focusDesk {
	desk := newFocusDesk(nil)
	desk.setMark(key, player)
	return desk
}

// The remote part carries focused only on the unit the mark names.
// Every other unit that lists the same controller carries no key, so the
// display draws one focus indicator per controller.
func TestDerivePlayerBusStatusMarksTheFocusedRemote(t *testing.T) {
	key := controllerKey("house", "sofa")
	cases := []struct {
		name  string
		focus *focusDesk
		want  bool
		held  bool
	}{
		{name: "the mark names this player", focus: focusedOn(key, "theater"), want: true, held: true},
		{name: "the mark names another player", focus: focusedOn(key, "console")},
		{name: "no mark at all", focus: newFocusDesk(nil)},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := derivePlayerBusStatus(theaterWithParts(),
				PlayerStatus{Activity: playerIdle}, nil, newPresenceDesk(nil), each.focus)
			remote := got.Components[len(got.Components)-1]
			mustMatch(t, remote.Focused != nil, each.held)
			mustMatch(t, remote.Focused != nil && *remote.Focused, each.want)
		})
	}
}

// Only a remote part carries focused. The display and the sinks are
// not a controller's to hold.
func TestDerivePlayerBusStatusMarksNoDisplayOrSinkFocused(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, newPresenceDesk(nil), focusedOn(controllerKey("house", "sofa"), "theater"))

	mustMatch(t, got.Components[0].Focused == nil, true)
	mustMatch(t, got.Components[1].Focused == nil, true)
}

// The key the display reads is focused, so the payload is pinned by
// its JSON and not only by its Go field.
func TestPlayerBusStatusCarriesTheFocusedKey(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, newPresenceDesk(nil), focusedOn(controllerKey("house", "sofa"), "theater"))

	payload, err := json.Marshal(got)
	mustSucceed(t, err)
	mustMatch(t, strings.Contains(string(payload), `"focused":true`), true)
}

// componentNames renders one status's parts as their names, so a test
// states the order in one line.
func componentNames(status playerBusStatus) []string {
	names := make([]string, 0, len(status.Components))
	for _, component := range status.Components {
		names = append(names, component.Name)
	}
	return names
}

// The presence fold: a controller the desk heard connected reads true, one
// it heard disconnected reads false, one whose pod went offline folds to
// false whatever its last presence said, and one the desk never heard from
// carries no connected key at all.
func TestDerivePlayerBusStatusFoldsThePresence(t *testing.T) {
	key := controllerKey("house", "sofa")
	offlinePod := connectedTo(key, true)
	offlinePod.setAvailability(key, false)
	onlinePod := connectedTo(key, true)
	onlinePod.setAvailability(key, true)

	cases := []struct {
		name string
		desk *presenceDesk
		want bool
		held bool
	}{
		{name: "a connected controller", desk: connectedTo(key, true), want: true, held: true},
		{name: "a disconnected controller", desk: connectedTo(key, false), held: true},
		{name: "a controller whose pod is offline", desk: offlinePod, held: true},
		{name: "a controller whose pod is online", desk: onlinePod, want: true, held: true},
		{name: "a controller never heard from", desk: newPresenceDesk(nil)},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, each.desk, newFocusDesk(nil))
			remote := got.Components[len(got.Components)-1]
			mustMatch(t, remote.Connected != nil, each.held)
			mustMatch(t, remote.Connected != nil && *remote.Connected, each.want)
		})
	}
}

// An idle unit names no Play, so the payload carries no play block and the
// screen draws the clock alone.
func TestDerivePlayerBusStatusCarriesNoPlayWhileIdle(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, newPresenceDesk(nil), newFocusDesk(nil))

	mustMatch(t, got.Play == nil, true)
}

// A Play that starts or runs carries its object name and the one line the
// screen draws.
func TestDerivePlayerBusStatusNamesTheStartingPlay(t *testing.T) {
	play := playOn("sailing", "house", "theater", phasePending)
	play.Spec.Items = []PlayItem{{URI: "https://nas/sailing.mkv", Presentation: &Presentation{Title: "Sailing"}}}

	got := derivePlayerBusStatus(theaterWithParts(),
		PlayerStatus{Activity: playerStarting, Play: "sailing"}, []Play{play}, newPresenceDesk(nil), newFocusDesk(nil))

	mustMatch(t, got.Activity, playerStarting)
	mustMatch(t, got.Play.Name, "sailing")
	mustMatch(t, got.Play.Title, "Sailing")
}

// The title resolves from the first item's Presentation: the Series names
// the show, the Title names a film, the Album names a record, and a first
// item that declares none of them falls back to the Play's own name.
func TestPlayTitleResolvesTheSeriesThenTheTitleThenTheName(t *testing.T) {
	cases := []struct {
		name         string
		presentation *Presentation
		want         string
	}{
		{
			name:         "a series wins",
			presentation: &Presentation{Series: "Adventure Time", Title: "Rainy Day Daydream"},
			want:         "Adventure Time",
		},
		{
			name:         "a title names a film",
			presentation: &Presentation{Title: "Sailing"},
			want:         "Sailing",
		},
		{
			name:         "an album names a record",
			presentation: &Presentation{Type: "music", Hint: "album", Album: "The Bends", Artist: "Radiohead"},
			want:         "The Bends",
		},
		{
			name:         "a title beats an album",
			presentation: &Presentation{Title: "OK Computer OKNOTOK", Album: "OK Computer"},
			want:         "OK Computer OKNOTOK",
		},
		{
			name:         "an empty presentation falls back to the name",
			presentation: &Presentation{Year: 1978},
			want:         "sailing",
		},
		{name: "no presentation falls back to the name", want: "sailing"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			play := playOn("sailing", "house", "theater", phaseRunning)
			play.Spec.Items = []PlayItem{{URI: "https://nas/sailing.mkv", Presentation: each.presentation}}
			mustMatch(t, playTitle(&play), each.want)
		})
	}
}

// A Play with no items at all still names itself, so a Play a person just
// created draws a line rather than a blank.
func TestPlayTitleFallsBackForAPlayWithNoItems(t *testing.T) {
	play := playOn("sailing", "house", "theater", phasePending)

	mustMatch(t, playTitle(&play), "sailing")
}

// testIdleBus is the bus block every unit in these tests carries: the
// broker the operator holds and the two topics the command pod stands
// on for the theater.
func testIdleBus() *PlayerIdleBus {
	return &PlayerIdleBus{
		Address:       testBusAddress,
		CommandsTopic: playerCommandsTopic(testTopicBase, "house", "theater"),
		ScreenTopic:   playerScreenTopic(testTopicBase, "house", "theater"),
	}
}

// the idle block reports the resolved controller and, where a
// controller draws, the claim a delegate references, the requests it
// carries, and the bus a delegate's client reads. A Player with no idle
// claim reports no block, and under media.liken.sh/none the block
// carries the controller alone.
func TestDeriveIdleStatus(t *testing.T) {
	player := standingIdlePlayer()
	drawOnly := standingIdlePlayer()
	drawOnly.Spec.Render = nil

	cases := []struct {
		name       string
		player     *Player
		controller string
		claim      *ResourceClaim
		want       *PlayerIdleStatus
	}{
		{
			name:       "no claim, so no block",
			player:     player,
			controller: idleControllerOwn,
			want:       nil,
		},
		{
			name:       "this operator draws",
			player:     player,
			controller: idleControllerOwn,
			claim:      buildIdleClaim(player, "display-draw"),
			want: &PlayerIdleStatus{
				Controller: idleControllerOwn,
				Claim:      "theater-idle-devices",
				Requests:   []string{"draw", "render"},
				Bus:        testIdleBus(),
			},
		},
		{
			name:       "a delegate draws",
			player:     player,
			controller: "library.liken.sh/media-browser",
			claim:      buildIdleClaim(player, "display-draw"),
			want: &PlayerIdleStatus{
				Controller: "library.liken.sh/media-browser",
				Claim:      "theater-idle-devices",
				Requests:   []string{"draw", "render"},
				Bus:        testIdleBus(),
			},
		},
		{
			name:       "a unit with no render node",
			player:     drawOnly,
			controller: "library.liken.sh/media-browser",
			claim:      buildIdleClaim(drawOnly, "display-draw"),
			want: &PlayerIdleStatus{
				Controller: "library.liken.sh/media-browser",
				Claim:      "theater-idle-devices",
				Requests:   []string{"draw"},
				Bus:        testIdleBus(),
			},
		},
		{
			name:       "nothing draws",
			player:     player,
			controller: idleControllerNone,
			claim:      buildIdleClaim(player, "display-draw"),
			want:       &PlayerIdleStatus{Controller: idleControllerNone},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := deriveIdleStatus(one.player, one.controller, testBusAddress, testTopicBase, one.claim)
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("idle = %+v, want %+v", got, one.want)
			}
		})
	}
}

// a Player that drives no screen, and a cluster that names no
// display-draw class, both leave the unit with no idle claim, which is
// what leaves the status with no idle block.
func TestIdleClaimForTheUnitsWithNoIdleScreen(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	if claim := media.idleClaimFor(standingIdlePlayer()); claim != nil {
		t.Errorf("a cluster with no display-draw class built %+v", claim.Metadata)
	}

	media.idleDisplayClass = "display-draw"
	speakerOnly := standingIdlePlayer()
	speakerOnly.Spec.Display = nil
	if claim := media.idleClaimFor(speakerOnly); claim != nil {
		t.Errorf("a Player with no screen built %+v", claim.Metadata)
	}
	if claim := media.idleClaimFor(standingIdlePlayer()); claim == nil {
		t.Error("a Player that drives a screen built no idle claim")
	}
}

// the pass writes the idle block onto the Player, so a delegate
// reads the claim and its requests from the status of the unit it draws.
func TestReconcilePlayersWritesTheIdleBlock(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	cluster.players["theater"] = player

	media.reconcilePlayers([]Player{*player}, nil, "America/New_York", nil)

	written := cluster.players["theater"].Status.Idle
	if written == nil {
		t.Fatalf("no idle block was written: %v", cluster.requests)
	}
	mustMatch(t, written.Controller, "library.liken.sh/media-browser")
	mustMatch(t, written.Claim, "theater-idle-devices")
	mustMatchAll(t, written.Requests, []string{"draw", "render"})
}
