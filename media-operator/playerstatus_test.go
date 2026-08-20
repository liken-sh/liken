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

func TestDerivePlayerStatus(t *testing.T) {
	cases := []struct {
		name  string
		plays []Play
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
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := derivePlayerStatus(theater(), one.plays)
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("status = %+v, want %+v", got, one.want)
			}
		})
	}
}
