package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func theater() *Player {
	return &Player{Metadata: ObjectMeta{Name: "theater", Namespace: "house"}}
}

// playOn builds a Play in a phase that targets one Player.
func playOn(name, namespace, player, phase string) Play {
	return Play{
		Metadata: ObjectMeta{Name: name, Namespace: namespace},
		Spec:     PlaySpec{Players: []string{player}},
		Status:   PlayStatus{Phase: phase},
	}
}

// reportsWithEnded builds reports with an ending report folded in for
// each run, as the bus reader folds one.
func reportsWithEnded(runs ...string) *reports {
	ended := newReports(nil)
	for _, run := range runs {
		namespace, name := splitRunKey(run)
		ended.fold(namespace, name, playReport{Item: 1, Ended: true})
	}
	return ended
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
			got := derivePlayerStatus(theater(), one.plays, reportsWithEnded(one.ended...))
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("the status is %+v, want %+v", got, one.want)
			}
		})
	}
}

// theaterWithParts is a Player with a display, a sink, and a remote,
// each with a displayName.
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

// holding builds a peripheralCache where the controller's claim names the
// device of the given Peripheral, and that Peripheral exists.
func holding(key string, peripheral Peripheral) *peripheralCache {
	peripherals := newPeripheralCache()
	peripherals.replace([]Peripheral{peripheral},
		map[string]string{key: peripheral.Metadata.Name})
	return peripherals
}

func linked(key string, connected bool) *peripheralCache {
	status := "False"
	if connected {
		status = conditionTrue
	}
	return holding(key, bonded("aa-bb-cc-dd-ee-ff", status))
}

func TestDerivePlayerBusStatusListsThePartsInScreenOrder(t *testing.T) {
	peripherals := linked(controllerKey("house", "sofa"), true)
	connected := true

	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, peripherals, newFocusCache(nil))

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
		t.Errorf("the status is %+v, want %+v", got, want)
	}
}

// A part with no displayName falls back to its DeviceClass, and a remote
// to the name of its Remote.
func TestDerivePlayerBusStatusFallsBackToTheClassAndTheName(t *testing.T) {
	player := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			Display: &PlayerDevice{Class: "display-output"},
			Sinks:   []PlayerDevice{{Class: "audio-sink"}},
			Remotes: []PlayerRemote{{Name: "sofa"}},
		},
	}

	got := derivePlayerBusStatus(player, PlayerStatus{Activity: playerIdle}, nil, newPeripheralCache(), newFocusCache(nil))

	mustMatch(t, got.DisplayName, "theater")
	mustMatchAll(t, componentNames(got), []string{"display-output", "audio-sink", "sofa"})
}

// focusedOn builds a focusCache whose mark for the controller names the
// Player.
func focusedOn(key, player string) *focusCache {
	focus := newFocusCache(nil)
	focus.setMark(key, player)
	return focus
}

func TestDerivePlayerBusStatusMarksTheFocusedRemote(t *testing.T) {
	key := controllerKey("house", "sofa")
	cases := []struct {
		name    string
		focus   *focusCache
		want    bool
		present bool
	}{
		{name: "the mark names this player", focus: focusedOn(key, "theater"), want: true, present: true},
		{name: "the mark names another player", focus: focusedOn(key, "console")},
		{name: "no mark at all", focus: newFocusCache(nil)},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := derivePlayerBusStatus(theaterWithParts(),
				PlayerStatus{Activity: playerIdle}, nil, newPeripheralCache(), each.focus)
			remote := got.Components[len(got.Components)-1]
			mustMatch(t, remote.Focused != nil, each.present)
			mustMatch(t, remote.Focused != nil && *remote.Focused, each.want)
		})
	}
}

func TestDerivePlayerBusStatusMarksNoDisplayOrSinkFocused(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, newPeripheralCache(), focusedOn(controllerKey("house", "sofa"), "theater"))

	mustMatch(t, got.Components[0].Focused == nil, true)
	mustMatch(t, got.Components[1].Focused == nil, true)
}

// The screen reads the JSON key, so this checks the JSON and not only
// the Go field.
func TestPlayerBusStatusCarriesTheFocusedKey(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, newPeripheralCache(), focusedOn(controllerKey("house", "sofa"), "theater"))

	payload, err := json.Marshal(got)
	mustSucceed(t, err)
	mustMatch(t, strings.Contains(string(payload), `"focused":true`), true)
}

func componentNames(status playerBusStatus) []string {
	names := make([]string, 0, len(status.Components))
	for _, component := range status.Components {
		names = append(names, component.Name)
	}
	return names
}

func TestDerivePlayerBusStatusFoldsTheLink(t *testing.T) {
	key := controllerKey("house", "sofa")
	// The controller's claim names a device that no Peripheral matches.
	gone := newPeripheralCache()
	gone.replace(nil, map[string]string{key: "aa-bb-cc-dd-ee-ff"})

	cases := []struct {
		name        string
		peripherals *peripheralCache
		want        bool
		present     bool
	}{
		{name: "a connected controller", peripherals: linked(key, true), want: true, present: true},
		{name: "a controller that is away", peripherals: linked(key, false), present: true},
		{name: "a controller whose peripheral is gone", peripherals: gone},
		{name: "a controller whose claim named none", peripherals: newPeripheralCache()},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, each.peripherals, newFocusCache(nil))
			remote := got.Components[len(got.Components)-1]
			mustMatch(t, remote.Connected != nil, each.present)
			mustMatch(t, remote.Connected != nil && *remote.Connected, each.want)
		})
	}
}

// A device that reports no battery level has no battery key.
func TestDerivePlayerBusStatusCarriesTheCharge(t *testing.T) {
	key := controllerKey("house", "sofa")
	charged := bonded("aa-bb-cc-dd-ee-ff", conditionTrue)
	charged.Status.Battery = &PeripheralBattery{Percentage: 62}

	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, holding(key, charged), newFocusCache(nil))

	remote := got.Components[len(got.Components)-1]
	mustMatch(t, remote.Battery != nil, true)
	mustMatch(t, *remote.Battery, 62)

	payload, err := json.Marshal(got)
	mustSucceed(t, err)
	mustMatch(t, strings.Contains(string(payload), `"battery":62`), true)

	flat := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle},
		nil, linked(key, true), newFocusCache(nil))
	mustMatch(t, flat.Components[len(flat.Components)-1].Battery == nil, true)
}

func TestDerivePlayerBusStatusCarriesNoPlayWhileIdle(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, newPeripheralCache(), newFocusCache(nil))

	mustMatch(t, got.Play == nil, true)
}

func TestDerivePlayerBusStatusNamesTheStartingPlay(t *testing.T) {
	play := playOn("sailing", "house", "theater", phasePending)
	play.Spec.Items = []PlayItem{{URI: "https://nas/sailing.mkv", Presentation: &Presentation{Title: "Sailing"}}}

	got := derivePlayerBusStatus(theaterWithParts(),
		PlayerStatus{Activity: playerStarting, Play: "sailing"}, []Play{play}, newPeripheralCache(), newFocusCache(nil))

	mustMatch(t, got.Activity, playerStarting)
	mustMatch(t, got.Play.Name, "sailing")
	mustMatch(t, got.Play.Title, "Sailing")
}

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

func TestPlayTitleFallsBackForAPlayWithNoItems(t *testing.T) {
	play := playOn("sailing", "house", "theater", phasePending)

	mustMatch(t, playTitle(&play), "sailing")
}

// testIdleBus is the bus block expected for the theater Player, which
// has no sinks, so no volume topic, and no remotes.
func testIdleBus() *PlayerIdleBus {
	return &PlayerIdleBus{
		Address:       testBusAddress,
		StatusTopic:   playerStatusTopic(testTopicBase, "house", "theater"),
		CommandsTopic: playerCommandsTopic(testTopicBase, "house", "theater"),
		PanelTopic:    playerPanelTopic(testTopicBase, "house", "theater"),
		PowerTopic:    playerPowerTopic(testTopicBase, "house", "theater"),
	}
}

func TestDeriveIdleStatus(t *testing.T) {
	player := playerWithDisplay()
	drawOnly := playerWithDisplay()
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
				Controller:       idleControllerOwn,
				Claim:            "theater-idle-devices",
				Requests:         []string{"draw", "render"},
				FadeAfterSeconds: defaultFadeAfterSeconds,
				Bus:              testIdleBus(),
			},
		},
		{
			name:       "a delegate draws",
			player:     player,
			controller: "library.liken.sh/media-browser",
			claim:      buildIdleClaim(player, "display-draw"),
			want: &PlayerIdleStatus{
				Controller:       "library.liken.sh/media-browser",
				Claim:            "theater-idle-devices",
				Requests:         []string{"draw", "render"},
				FadeAfterSeconds: defaultFadeAfterSeconds,
				Bus:              testIdleBus(),
			},
		},
		{
			name:       "a unit with no render node",
			player:     drawOnly,
			controller: "library.liken.sh/media-browser",
			claim:      buildIdleClaim(drawOnly, "display-draw"),
			want: &PlayerIdleStatus{
				Controller:       "library.liken.sh/media-browser",
				Claim:            "theater-idle-devices",
				Requests:         []string{"draw"},
				FadeAfterSeconds: defaultFadeAfterSeconds,
				Bus:              testIdleBus(),
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
			got := deriveIdleStatus(one.player, one.controller, testBusAddress, testTopicBase,
				one.claim, resolveIdle(nil, nil, testIdleImage),
				gatherIdleRemotes(one.player, testTopicBase))
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("the idle block is %+v, want %+v", got, one.want)
			}
		})
	}
}

func TestIdleClaimForTheUnitsWithNoIdleScreen(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	if claim := media.idleClaimFor(playerWithDisplay()); claim != nil {
		t.Errorf("a cluster with no display-draw class built the claim %+v, want none", claim.Metadata)
	}

	media.idleDisplayClass = "display-draw"
	speakerOnly := playerWithDisplay()
	speakerOnly.Spec.Display = nil
	if claim := media.idleClaimFor(speakerOnly); claim != nil {
		t.Errorf("a Player with no screen built the claim %+v, want none", claim.Metadata)
	}
	if claim := media.idleClaimFor(playerWithDisplay()); claim == nil {
		t.Error("a Player that drives a screen built no idle claim, want one")
	}
}

func TestReconcilePlayersWritesTheIdleBlock(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	player.Spec.Idle = &IdlePolicy{Controller: "library.liken.sh/media-browser"}
	cluster.players["theater"] = player

	media.reconcilePlayers([]Player{*player}, nil, "America/New_York", nil)

	written := cluster.players["theater"].Status.Idle
	if written == nil {
		t.Fatalf("the Player has no status.idle after reconcilePlayers, want one (requests: %v)", cluster.requests)
	}
	mustMatch(t, written.Controller, "library.liken.sh/media-browser")
	mustMatch(t, written.Claim, "theater-idle-devices")
	mustMatchAll(t, written.Requests, []string{"draw", "render"})
	mustMatch(t, written.FadeAfterSeconds, defaultFadeAfterSeconds)
	mustMatch(t, written.Bus.StatusTopic, playerStatusTopic(testTopicBase, "house", "theater"))
	mustMatch(t, written.Bus.PanelTopic, playerPanelTopic(testTopicBase, "house", "theater"))
}

func TestTheIdleBusCarriesTheLevelTopicOnlyWithSinks(t *testing.T) {
	cases := []struct {
		name       string
		sinks      []PlayerDevice
		wantVolume string
	}{
		{name: "a unit with nothing to hear"},
		{
			name:       "a unit with speakers",
			sinks:      []PlayerDevice{{Class: "audio-sink"}},
			wantVolume: playerVolumeTopic(testTopicBase, "house", "theater"),
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			player := playerWithDisplay()
			player.Spec.Sinks = each.sinks

			got := deriveIdleStatus(player, idleControllerOwn, testBusAddress, testTopicBase,
				buildIdleClaim(player, "display-draw"), resolveIdle(nil, nil, testIdleImage),
				gatherIdleRemotes(player, testTopicBase))

			mustMatch(t, got.Bus.VolumeTopic, each.wantVolume)
		})
	}
}

func TestDeriveIdleStatusCarriesTheLevelAndTheRemotes(t *testing.T) {
	player := playerWithDisplay()
	player.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}, {Name: "armchair"}}

	got := deriveIdleStatus(player, idleControllerOwn, testBusAddress, testTopicBase,
		buildIdleClaim(player, "display-draw"), resolveIdle(nil, nil, testIdleImage),
		gatherIdleRemotes(player, testTopicBase))

	mustMatch(t, got.Bus.VolumeTopic, playerVolumeTopic(testTopicBase, "house", "theater"))
	want := []PlayerIdleRemote{
		{
			Events: remoteEventsTopic(testTopicBase, "house", "sofa"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "sofa"),
		},
		{
			Events: remoteEventsTopic(testTopicBase, "house", "armchair"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "armchair"),
		},
	}
	if !reflect.DeepEqual(got.Bus.Remotes, want) {
		t.Errorf("the bus remotes are %+v, want %+v", got.Bus.Remotes, want)
	}
}
