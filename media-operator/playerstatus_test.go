package main

// A Player's status is derived from the Plays that name it, so these
// tests hand the derivation a Player and a list of Plays and check
// the one status it earns.

import (
	"reflect"
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

	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, desk)

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

	got := derivePlayerBusStatus(player, PlayerStatus{Activity: playerIdle}, nil, newPresenceDesk(nil))

	mustMatch(t, got.DisplayName, "theater")
	mustMatchAll(t, componentNames(got), []string{"display-output", "audio-sink", "sofa"})
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
			got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, each.desk)
			remote := got.Components[len(got.Components)-1]
			mustMatch(t, remote.Connected != nil, each.held)
			mustMatch(t, remote.Connected != nil && *remote.Connected, each.want)
		})
	}
}

// An idle unit names no Play, so the payload carries no play block and the
// screen draws the clock alone.
func TestDerivePlayerBusStatusCarriesNoPlayWhileIdle(t *testing.T) {
	got := derivePlayerBusStatus(theaterWithParts(), PlayerStatus{Activity: playerIdle}, nil, newPresenceDesk(nil))

	mustMatch(t, got.Play == nil, true)
}

// A Play that starts or runs carries its object name and the one line the
// screen draws.
func TestDerivePlayerBusStatusNamesTheStartingPlay(t *testing.T) {
	play := playOn("sailing", "house", "theater", phasePending)
	play.Spec.Items = []PlayItem{{URI: "https://nas/sailing.mkv", Presentation: &Presentation{Title: "Sailing"}}}

	got := derivePlayerBusStatus(theaterWithParts(),
		PlayerStatus{Activity: playerStarting, Play: "sailing"}, []Play{play}, newPresenceDesk(nil))

	mustMatch(t, got.Activity, playerStarting)
	mustMatch(t, got.Play.Name, "sailing")
	mustMatch(t, got.Play.Title, "Sailing")
}

// The title resolves from the first item's Presentation: the Series names
// the show, the Title names a film, and a first item that declares neither
// falls back to the Play's own name.
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
