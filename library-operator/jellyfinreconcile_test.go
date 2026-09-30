package main

// What these tests prove. The reconcile reads every user's played and
// resumable items and publishes each as one outside play, dated at its
// last play in Jellyfin, so the progress store takes what changed while a
// role was down and keeps what is newer. It writes nothing to Jellyfin,
// and it skips a position the role wrote itself. It runs when the progress
// role is online and the webhook listener is up, and not before.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// A person marked a film played in Jellyfin while the role was down.
func toggledWhileDown() *fakeBackfillJellyfin {
	return &fakeBackfillJellyfin{
		users: []jellyfinUser{{Name: "Person-A", ID: "user-a"}},
		played: map[string][]jellyfinItem{"user-a": {{ID: "item-film", Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": "1101"}, RunTimeTicks: 81_600_000_000,
			UserData: jellyfinUserData{Played: true, LastPlayedDate: "2026-09-08T19:30:00.0000000Z"}}}},
		series: backfillSeries(),
	}
}

// One role over that Jellyfin, with a recording publish function.
func standReconcileRole(t *testing.T, fake *fakeBackfillJellyfin) (*jellyfin, *jellyfinMessages) {
	t.Helper()
	server := apiservertest.Start(t, fake)
	messages := &jellyfinMessages{}
	role := newJellyfinOn("house", defaultTopicBase, defaultMediaTopicBase, "127.0.0.1:0",
		newJellyfinAPI(apiservertest.Host, "the-key", server.Client()), newJellyfinClock().now,
		messages.publish, &strings.Builder{})
	return role, messages
}

// A toggle made while the role was down arrives as one outside play, dated
// at the toggle, which the progress role records where it is newer than
// the row.
func TestTheReconcileRecordsAToggleMadeWhileTheRoleWasDown(t *testing.T) {
	role, messages := standReconcileRole(t, toggledWhileDown())

	role.reconcile(t.Context())

	topic, play := messages.outside(t)
	if topic != playOutsideTopic(defaultTopicBase, "house", "jellyfin-user-a-item-film") {
		t.Errorf("topic = %q, want the outside play of the person and the item", topic)
	}
	want := time.Date(2026, 9, 8, 19, 30, 0, 0, time.UTC).Unix()
	if play.Position != 8160 || !play.Ended || play.At != want {
		t.Errorf("play = %+v, want the film played to its end at the toggle", play)
	}
}

// The reconcile reads Jellyfin and writes nothing back to it. What it
// read goes to the progress store alone, and the role reads no outside
// play.
func TestTheReconcileWritesNothingToJellyfin(t *testing.T) {
	fake := toggledWhileDown()
	role, _ := standReconcileRole(t, fake)

	role.reconcile(t.Context())

	for _, query := range fake.queries {
		if strings.HasPrefix(query, "/UserItems/") {
			t.Errorf("the reconcile asked %q, want no user-data request", query)
		}
	}
}

// A position the role wrote in the last few minutes is its own write read
// back, and the reconcile skips it, the way the webhook drops its echo.
func TestTheReconcileSkipsThePositionTheRoleWrote(t *testing.T) {
	role, messages := standReconcileRole(t, toggledWhileDown())
	role.out.echoes.remember("user-a", "item-film", 8160)

	role.reconcile(t.Context())

	if len(messages.held) != 0 {
		t.Errorf("messages = %+v, want none", messages.held)
	}
}

// The progress role's online starts the reconcile once the listener is up.
func TestTheProgressRoleOnlineStartsTheReconcile(t *testing.T) {
	role, messages := standReconcileRole(t, toggledWhileDown())
	role.bus = newBus("nowhere", "jellyfin-house", nil, nil, role.onMessage)
	jellyfinTicksEvery(t, time.Hour)

	role.onMessage(progressAvailabilityTopic(defaultTopicBase, "house"), []byte(availabilityOnline))
	stopped, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	go func() {
		for stopped.Err() == nil && messages.count() == 0 {
			time.Sleep(time.Millisecond)
		}
		stop()
	}()
	role.serve(stopped)

	if messages.count() != 1 {
		t.Errorf("messages = %+v, want the one outside play", messages.held)
	}
}

// The progress role's offline starts nothing, because an outside play is
// not retained and a progress role that is down loses it.
func TestTheProgressRoleOfflineStartsNothing(t *testing.T) {
	role, _ := standReconcileRole(t, toggledWhileDown())

	role.onMessage(progressAvailabilityTopic(defaultTopicBase, "house"), []byte(availabilityOffline))

	if len(role.reconciles) != 0 {
		t.Error("an offline asked for a reconcile")
	}
}

// A role whose listener cannot start runs no reconcile, because a toggle
// made during the read would reach no webhook.
func TestARoleThatCannotListenRunsNoReconcile(t *testing.T) {
	role, messages := standReconcileRole(t, toggledWhileDown())
	role.bus = newBus("nowhere", "jellyfin-house", nil, nil, role.onMessage)
	role.listen = "256.256.256.256:8080"

	role.onMessage(progressAvailabilityTopic(defaultTopicBase, "house"), []byte(availabilityOnline))
	role.serve(t.Context())

	if messages.count() != 0 {
		t.Errorf("messages = %+v, want none", messages.held)
	}
}

// How many messages the role published.
func (m *jellyfinMessages) count() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return len(m.held)
}
