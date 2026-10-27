package main

// These tests drive the arrangement protocol's reports through one
// placement pass: what the Display's status shows about each claim's
// arranger, and the Events a person reads when a claim gains or loses
// it. The module is the fake of placementbench_test.go, and the
// Events land in the fixture's eventstest store.

import (
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// The living room of a film with its display over it: one claim, two
// pods, three windows. mpv draws the film and its subtitles as two
// windows with one app_id, and the display draws the controls.
type livingRoom struct {
	*placementFixture
	film string
}

func newLivingRoom(t *testing.T) livingRoom {
	t.Helper()
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "player-0")
	fixture.pod("living-room", "player-0", map[string]string{"app": "player"})
	for _, appID := range []string{"liken-film", "liken-film", "liken-display"} {
		id := fixture.surface(film, 1920, 1080)
		fixture.reports(surfaceAppIDLine(id, appID), func(state layoutState) bool {
			return state.Surfaces[id].AppID == appID
		})
	}
	fixture.record()
	return livingRoom{placementFixture: fixture, film: film}
}

func surfaceAppIDLine(id int, appID string) string {
	return layoutSurfaceAppIDEvent + " " + strconv.Itoa(id) + " " + appID
}

// reports sends one line from the module and waits until the store
// holds what it says.
func (f *placementFixture) reports(line string, held func(layoutState) bool) {
	f.t.Helper()
	f.module.send(line)
	waitUntil(f.t, line+" reaches the store", func() bool { return held(f.link.state()) })
}

// The module grants the film claim's arranger, and the arranger
// commits an arrangement.
func (r livingRoom) grant(pid string) {
	r.t.Helper()
	r.reports(layoutArrangerEvent+" "+r.film+" "+pid, func(state layoutState) bool {
		_, held := state.Arrangers[r.film]
		return held
	})
}

func (r livingRoom) commit(name string) {
	r.t.Helper()
	r.reports(layoutArrangementEvent+" "+r.film+" 7 "+name, func(state layoutState) bool {
		return state.Arrangers[r.film].Arrangement == name
	})
}

// release sends the module's report that the arranger left, with the
// reason it gives, or with none, the way an older module sends it.
func (r livingRoom) release(reason string) {
	r.t.Helper()
	r.reports(strings.TrimSpace(layoutArrangerGoneEvent+" "+r.film+" "+reason), func(state layoutState) bool {
		_, held := state.Arrangers[r.film]
		return !held
	})
}

// record gives the pass a recorder whose Events the fixture keeps.
func (f *placementFixture) record() {
	f.pass.displays.recorder = events.New(f.t.Context(), f.client, componentName,
		events.Options{Instance: "display-operator-test", Log: io.Discard})
}

// posted answers the arrangers' Events the recorder wrote about the
// lab screen since the last call. Shutdown returns after the recorder's last
// request, so the answer needs no wait on the clock, and a fresh
// recorder takes the next pass's Events.
func (f *placementFixture) posted() []string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 5*time.Second)
	defer cancel()
	if err := f.pass.displays.recorder.Shutdown(ctx); err != nil {
		f.t.Fatal(err)
	}
	f.record()
	var lines []string
	for _, event := range f.events.About("Display", labDisplayName()) {
		line := eventLines([]events.Event{event})[0]
		if strings.HasPrefix(event.Reason, "Arranger") && !slices.Contains(f.seen, line) {
			lines = append(lines, line)
		}
	}
	f.seen = append(f.seen, lines...)
	return lines
}

func TestTheDisplayShowsWhoArrangesEachClaim(t *testing.T) {
	room := newLivingRoom(t)
	room.grant("0")
	room.commit("credits")

	room.run()

	status := room.status(labMonitor())
	want := []DisplayArranger{{
		Claim:           "living-room/film",
		Region:          defaultLayoutName,
		Pods:            []string{"living-room/player-0"},
		Arrangement:     "credits",
		DuplicateAppIDs: []string{"liken-film"},
	}}
	if !slices.EqualFunc(status.Arrangers, want, sameArranger) {
		t.Errorf("the arrangers are %+v, want %+v", status.Arrangers, want)
	}
	var appIDs []string
	for _, surface := range status.Surfaces {
		appIDs = append(appIDs, surface.AppID)
	}
	if !slices.Equal(appIDs, []string{"liken-film", "liken-film", "liken-display"}) {
		t.Errorf("the windows carry %q, want each one's app_id", appIDs)
	}
}

func sameArranger(a, b DisplayArranger) bool {
	return a.Claim == b.Claim && a.Region == b.Region && slices.Equal(a.Pods, b.Pods) &&
		a.PID == b.PID && a.Arrangement == b.Arrangement && slices.Equal(a.DuplicateAppIDs, b.DuplicateAppIDs)
}

// A commit follows a key press. One that keeps the name changes
// nothing in status, so it costs the API server no write, and one that
// changes the name writes it once.
func TestOnlyANewArrangementNameWritesTheStatus(t *testing.T) {
	room := newLivingRoom(t)
	room.grant("0")
	room.commit("credits")
	room.run()
	version := room.displays[labDisplayName()].Metadata.ResourceVersion

	room.reports(layoutArrangementEvent+" "+room.film+" 8 credits", func(layoutState) bool { return true })
	room.run()
	if now := room.displays[labDisplayName()].Metadata.ResourceVersion; now != version {
		t.Errorf("a commit of the same arrangement wrote the status (%s became %s)", version, now)
	}

	room.commit("film")
	room.run()
	if got := room.status(labMonitor()).Arrangers[0].Arrangement; got != "film" {
		t.Errorf("the arrangement is %q, want film", got)
	}
}

// A grant posts one Normal Event, and an arrangement posts none,
// because commits follow key presses.
func TestAGrantPostsOneEventAndACommitPostsNone(t *testing.T) {
	room := newLivingRoom(t)
	room.grant("0")
	room.run()
	room.commit("credits")
	room.run()
	room.commit("film")
	room.run()

	want := []string{
		"Normal ArrangerGranted a client of claim living-room/film in living-room/player-0 arranges the claim's surfaces in region default",
	}
	if got := room.posted(); !slices.Equal(got, want) {
		t.Errorf("the Events are\n%q\nwant\n%q", got, want)
	}
}

// The arranger leaves, and the reason the module gives and the pods
// that hold its claim decide the Event. A client that disconnected
// while its pod still runs is how a crash looks, which a person may
// need to look at. A client that released the region on purpose, and
// pods that are gone or going, are a workload doing what it meant to.
func TestALostArrangerIsAWarningOnlyForADisconnectWhileItsPodRuns(t *testing.T) {
	deleting := "2026-10-27T12:00:00Z"
	const (
		lost       = "Warning ArrangerLost the arranger of claim living-room/film disconnected while pod living-room/player-0 still runs, so the claim's surfaces return to fill region default"
		withPods   = "Normal ArrangerReleased the arranger of claim living-room/film left with its pods, so the claim's surfaces return to fill region default"
		gaveItUp   = "Normal ArrangerReleased the arranger of claim living-room/film gave up the region, so the claim's surfaces return to fill region default"
		podRuns    = ""
		podDeleted = "deleting"
		podGone    = "gone"
	)
	cases := []struct {
		name   string
		reason string
		pod    string
		want   string
	}{
		{"a disconnect while the pod runs", arrangerDisconnected, podRuns, lost},
		{"a line with no reason while the pod runs", "", podRuns, lost},
		{"a release while the pod runs", arrangerReleased, podRuns, gaveItUp},
		{"a disconnect while the pod is being deleted", arrangerDisconnected, podDeleted, withPods},
		{"a disconnect after the pod is gone", arrangerDisconnected, podGone, withPods},
		{"a release after the pod is gone", arrangerReleased, podGone, gaveItUp},
	}
	pods := map[string]func(room livingRoom){
		podRuns: func(livingRoom) {},
		podDeleted: func(room livingRoom) {
			room.pods["living-room/player-0"].Metadata.DeletionTimestamp = &deleting
		},
		podGone: func(room livingRoom) { delete(room.pods, "living-room/player-0") },
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			room := newLivingRoom(t)
			room.grant("0")
			room.run()
			room.posted()

			room.release(tc.reason)
			pods[tc.pod](room)
			room.run()

			if got := room.posted(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("the Events are\n%q\nwant\n%q", got, []string{tc.want})
			}
			if got := room.status(labMonitor()).Arrangers; len(got) != 0 {
				t.Errorf("status still shows %+v", got)
			}
		})
	}
}

// A compositor that restarts takes every arranger with it, and the
// pass on the new connection posts no loss for them: the restart has
// its own report. A client that binds again posts its grant again.
func TestARestartedCompositorPostsNoLostArranger(t *testing.T) {
	room := newLivingRoom(t)
	room.grant("0")
	room.run()
	room.posted()

	room.module.drop()
	room.module.accepted()
	waitUntil(t, "the link serves the second connection", room.link.moduleServing)
	room.run()

	if got := room.posted(); len(got) != 0 {
		t.Errorf("the restart posted %q, want nothing", got)
	}
	if got := room.status(labMonitor()).Arrangers; len(got) != 0 {
		t.Errorf("status still shows %+v after the restart", got)
	}
}

// The compositor reads a pid only for a client in its own pid
// namespace, and status shows it when it has one.
func TestAnArrangerWithAPidShowsIt(t *testing.T) {
	room := newLivingRoom(t)
	room.grant("812")

	room.run()

	if got := room.status(labMonitor()).Arrangers[0].PID; got != 812 {
		t.Errorf("the arranger's pid is %d, want 812", got)
	}
	want := []string{
		"Normal ArrangerGranted a client of claim living-room/film in living-room/player-0 (pid 812) arranges the claim's surfaces in region default",
	}
	if got := room.posted(); !slices.Equal(got, want) {
		t.Errorf("the Events are\n%q\nwant\n%q", got, want)
	}
}
