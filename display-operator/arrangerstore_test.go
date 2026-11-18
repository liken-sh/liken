package main

// These tests call apply directly, because layoutstore_test.go covers
// the socket.

import (
	"maps"
	"testing"
)

// storeAfter returns a store after it applies each line in order. It
// fails the test if the store cannot read a line.
func storeAfter(t *testing.T, lines ...string) *layoutLink {
	t.Helper()
	link := newLayoutLink("")
	for _, line := range lines {
		if !link.apply(line) {
			t.Fatalf("the store rejects the line %q, want it applied", line)
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
			name:  "an app_id is recorded on its surface",
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
			name:      "a grant records the pid the compositor read",
			lines:     []string{"arranger wayland-film 812"},
			arrangers: map[string]layoutArranger{"wayland-film": {Socket: "wayland-film", PID: 812}},
		},
		{
			name:  "a commit records the arrangement name",
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
			name:  "an arranger that leaves is removed and its reason recorded",
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
				t.Errorf("surface 4 has app_id %q, want %q", got, tc.appID)
			}
		})
	}
}

// The store must reject each of these lines, so a corrupt line cannot
// create an arranger or an app_id for something that does not exist.
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
		{"a departure with no socket", "arranger-gone"},
		{"a departure with an unknown reason", "arranger-gone wayland-film crashed"},
		{"a departure with a token too many", "arranger-gone wayland-film released now"},
		{"a commit by a claim with no arranger", "arrangement wayland-idle 1 credits"},
		{"a commit whose serial is not a number", "arrangement wayland-film first credits"},
		{"a commit with no serial", "arrangement wayland-film"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := storeAfter(t, "surface 4 wayland-film 1920 1080", "arranger wayland-film 812")
			if link.apply(tc.line) {
				t.Errorf("the store accepts the line %q, want it refused", tc.line)
			}
			want := map[string]layoutArranger{"wayland-film": {Socket: "wayland-film", PID: 812}}
			if got := link.state().Arrangers; !maps.Equal(got, want) {
				t.Errorf("the arrangers are %+v, want %+v", got, want)
			}
		})
	}
}

// The module reports its whole state again on each new connection, so
// opened empties the store.
func TestANewConnectionClearsEveryArranger(t *testing.T) {
	link := storeAfter(t, "arranger wayland-film 812", "arranger wayland-idle 0", "arranger-gone wayland-idle released")

	link.opened()

	if state := link.state(); len(state.Arrangers)+len(state.Departures) != 0 {
		t.Errorf("after a new connection the store holds arrangers %+v and departures %+v, want none", state.Arrangers, state.Departures)
	}
}
