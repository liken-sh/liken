package main

// These tests feed the store the arrangement protocol's report lines,
// one table row at a time, and read what it holds. The lines go
// straight to the store's one writer, because the socket is the link's
// business and layoutstore_test.go drives it.

import (
	"maps"
	"testing"
)

// storeAfter answers a store that read each line in turn, and fails
// the test on a line the store did not read.
func storeAfter(t *testing.T, lines ...string) *layoutLink {
	t.Helper()
	link := newLayoutLink("")
	for _, line := range lines {
		if !link.remember(line) {
			t.Fatalf("the store did not read %q", line)
		}
	}
	return link
}

func TestTheStoreReadsTheArrangementReports(t *testing.T) {
	const film = "surface 4 wayland-film 1920 1080"
	cases := []struct {
		name       string
		lines      []string
		arrangers  map[string]layoutArranger
		departures map[string]string
		appID      string
	}{
		{
			name:  "an app_id names a surface",
			lines: []string{film, "surface-app-id 4 liken-film"},
			appID: "liken-film",
		},
		{
			name:  "a later app_id replaces the first",
			lines: []string{film, "surface-app-id 4 mpv", "surface-app-id 4 liken-film"},
			appID: "liken-film",
		},
		{
			name:  "an empty app_id clears it",
			lines: []string{film, "surface-app-id 4 liken-film", "surface-app-id 4"},
		},
		{
			name:      "a grant holds the pid the compositor read",
			lines:     []string{"arranger wayland-film 812"},
			arrangers: map[string]layoutArranger{"wayland-film": {Socket: "wayland-film", PID: 812}},
		},
		{
			name:  "a commit names the arrangement",
			lines: []string{"arranger wayland-film 812", "arrangement wayland-film 3 credits"},
			arrangers: map[string]layoutArranger{
				"wayland-film": {Socket: "wayland-film", PID: 812, Arrangement: "credits"},
			},
		},
		{
			name:  "a new grant starts with no arrangement",
			lines: []string{"arranger wayland-film 812", "arrangement wayland-film 3 credits", "arranger wayland-film 0"},
			arrangers: map[string]layoutArranger{
				"wayland-film": {Socket: "wayland-film"},
			},
		},
		{
			name:  "an arranger that goes leaves the store with its reason",
			lines: []string{"arranger wayland-film 812", "arranger wayland-idle 0", "arranger-gone wayland-film released"},
			arrangers: map[string]layoutArranger{
				"wayland-idle": {Socket: "wayland-idle"},
			},
			departures: map[string]string{"wayland-film": arrangerReleased},
		},
		{
			name:       "a departure with no reason is a disconnect",
			lines:      []string{"arranger wayland-film 812", "arranger-gone wayland-film"},
			departures: map[string]string{"wayland-film": arrangerDisconnected},
		},
		{
			name:      "a new grant clears the departure",
			lines:     []string{"arranger wayland-film 812", "arranger-gone wayland-film disconnected", "arranger wayland-film 0"},
			arrangers: map[string]layoutArranger{"wayland-film": {Socket: "wayland-film"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := storeAfter(t, tc.lines...).state()
			if !maps.Equal(state.Arrangers, tc.arrangers) {
				t.Errorf("the arrangers are %+v, want %+v", state.Arrangers, tc.arrangers)
			}
			if !maps.Equal(state.Departures, tc.departures) {
				t.Errorf("the departures are %+v, want %+v", state.Departures, tc.departures)
			}
			if got := state.Surfaces[4].AppID; got != tc.appID {
				t.Errorf("surface 4 carries app_id %q, want %q", got, tc.appID)
			}
		})
	}
}

// Each line below is a report the store must not take, so a corrupt
// line never names an arranger or a window that is not there.
func TestTheStoreDropsAnArrangementReportItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"an app_id for a surface the store never saw", "surface-app-id 9 liken-film"},
		{"an app_id for a surface id that is not a number", "surface-app-id four liken-film"},
		{"a grant with no pid", "arranger wayland-film"},
		{"a grant whose pid is not a number", "arranger wayland-film self"},
		{"a grant with a negative pid", "arranger wayland-film -1"},
		{"a grant with a token too many", "arranger wayland-film 812 extra"},
		{"a release with no socket", "arranger-gone"},
		{"a release with a reason the protocol does not state", "arranger-gone wayland-film crashed"},
		{"a release with a token too many", "arranger-gone wayland-film released now"},
		{"a commit by a claim with no arranger", "arrangement wayland-idle 1 credits"},
		{"a commit whose serial is not a number", "arrangement wayland-film first credits"},
		{"a commit with no serial", "arrangement wayland-film"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := storeAfter(t, "surface 4 wayland-film 1920 1080", "arranger wayland-film 812")
			if link.remember(tc.line) {
				t.Errorf("the store read %q", tc.line)
			}
			want := map[string]layoutArranger{"wayland-film": {Socket: "wayland-film", PID: 812}}
			if got := link.state().Arrangers; !maps.Equal(got, want) {
				t.Errorf("the arrangers are %+v, want %+v", got, want)
			}
		})
	}
}

// A new connection is a new compositor, and the arrangers went with
// the old one.
func TestANewConnectionForgetsEveryArranger(t *testing.T) {
	link := storeAfter(t, "arranger wayland-film 812", "arranger wayland-idle 0", "arranger-gone wayland-idle released")

	link.opened()

	if state := link.state(); len(state.Arrangers)+len(state.Departures) != 0 {
		t.Errorf("the store holds %+v and %+v after a new connection, want nothing", state.Arrangers, state.Departures)
	}
}
