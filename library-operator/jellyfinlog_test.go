package main

// These tests read the lines the Jellyfin role leaves for what a person did:
// a play that started, stopped, or was marked by hand in Jellyfin, and the
// final position of a Play of this cluster written back to Jellyfin. The
// progress of a play that runs on leaves no line.

import (
	"strings"
	"testing"
)

func TestAPersonsJellyfinEventLeavesOneLine(t *testing.T) {
	movie := func(event string) jellyfinEvent {
		return jellyfinEvent{Event: event, User: "person-a", UserID: "u1", ItemID: "i1", ItemType: "Movie",
			PositionTicks: "42100000000", RunTimeTicks: "81600000000", Tmdb: "1001"}
	}
	marked := movie(jellyfinUserDataEvent)
	marked.SaveReason, marked.Played = jellyfinToggleReason, "True"
	cases := []struct {
		name  string
		event jellyfinEvent
		want  string
	}{
		{"a start", movie(jellyfinStartEvent), "jellyfin PlaybackStart for person person-a, user u1, item i1, " +
			"of tmdb:1001: published position 1:10:10 of 2:16:00, ended false"},
		{"a stop", movie(jellyfinStopEvent), "jellyfin PlaybackStop for person person-a, user u1, item i1, " +
			"of tmdb:1001: published position 1:10:10 of 2:16:00, ended true"},
		{"a hand mark", marked, "jellyfin UserDataSaved for person person-a, user u1, item i1, " +
			"of tmdb:1001: published position 2:16:00 of 2:16:00, ended false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			role, _, logged := standJellyfinRole(t, jellyfinFixture())

			jellyfinPosted(t, role, jellyfinPost(t, c.event))

			if got := strings.Count(logged.String(), c.want); got != 1 {
				t.Errorf("lines = %d, want one %q in:\n%s", got, c.want, logged)
			}
		})
	}
}

// Jellyfin posts the progress of a play every ten seconds, and each one is
// published and leaves no line.
func TestJellyfinProgressLeavesNoLine(t *testing.T) {
	role, messages, logged := standJellyfinRole(t, jellyfinFixture())
	event := jellyfinEvent{Event: jellyfinProgressEvent, User: "person-a", UserID: "u1", ItemID: "i1",
		ItemType: "Movie", PositionTicks: "42100000000", RunTimeTicks: "81600000000", Tmdb: "1001"}

	jellyfinPosted(t, role, jellyfinPost(t, event))

	messages.outside(t)
	if logged.Len() != 0 {
		t.Errorf("logged %q, want nothing", logged)
	}
}

// The final position of a Play leaves one line per person, and the ticks
// before it leave none.
func TestAFinalWriteLeavesOneLine(t *testing.T) {
	fake := &fakeJellyfin{
		users: []jellyfinUser{{Name: "person-a", ID: "user-a"}},
		items: []jellyfinItem{{ID: "item-a", Type: "Movie", ProviderIds: map[string]string{"Tmdb": "1001"}}},
	}
	out, logged := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1001"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())
	if logged.Len() != 0 {
		t.Fatalf("a tick logged %q, want nothing", logged)
	}

	out.final(t.Context(), "play-1", []byte(`{"phase":"Finished","item":0,"position":"2:16:00","duration":"2:16:00"}`))

	want := "wrote the final position 2:16:00 of the Play of tmdb:1001 to jellyfin for person person-a: " +
		"item item-a, user user-a, played true"
	if got := strings.Count(logged.String(), want); got != 1 {
		t.Errorf("lines = %d, want one %q in:\n%s", got, want, logged)
	}
}
