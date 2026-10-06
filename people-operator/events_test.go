package main

// These tests read the Events the operator posts about a Person. Each
// change of AvatarReady posts one Event with the condition's reason,
// and a new picture that leaves the condition as it was posts
// AvatarUpdated. A Person is cluster-scoped, so its Events are in
// default.

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// posted is the part of an Event a test compares: its type, its
// reason, and its count.
type posted struct {
	kind, reason string
	count        int32
}

// postedAbout answers what the operator posted about one Person, in
// the order it posted it.
func (w *world) postedAbout(name string) []posted {
	var out []posted
	for _, event := range w.api.recorded.About(personKind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Count})
	}
	return out
}

// Each change of AvatarReady posts one Event, a failed read a Warning,
// and a new picture under the same reason posts AvatarUpdated.
func TestEachChangeOfThePicturePostsOneEvent(t *testing.T) {
	cases := []struct {
		name   string
		second servedPicture
		want   []posted
	}{
		{"the same picture", servedPicture{etag: `"one"`}, []posted{
			{events.TypeNormal, reasonFetched, 1},
		}},
		{"a new picture", servedPicture{etag: `"two"`}, []posted{
			{events.TypeNormal, reasonFetched, 1},
			{events.TypeNormal, reasonAvatarUpdated, 1},
		}},
		{"a failed read", servedPicture{status: http.StatusInternalServerError}, []posted{
			{events.TypeNormal, reasonFetched, 1},
			{events.TypeWarning, reasonFetchFailed, 1},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
				w.api.putPerson(ada("https://pictures.example/ada.png"))
				w.settle()

				second := c.second
				if second.status == 0 {
					second.body = solid(t, 64, 64, blue)
				}
				w.pictures.serve("/ada.png", second)
				time.Sleep(recheckInterval)
				w.settle()

				if got := w.postedAbout("ada"); !slices.Equal(got, c.want) {
					t.Errorf("the Events about ada = %v, want %v", got, c.want)
				}
			})
		})
	}
}

// A failed read's Event says the last good picture stays, and a read
// that works again posts the recovery. The recovery repeats the first
// Fetched within ten minutes, so the recorder counts it on that Event.
func TestARecoveredSourcePostsItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.settle()
		w.pictures.serve("/ada.png", servedPicture{status: http.StatusInternalServerError})
		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "again"))
		w.settle()
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "and again"))
		w.settle()

		want := []posted{
			{events.TypeNormal, reasonFetched, 2},
			{events.TypeWarning, reasonFetchFailed, 1},
		}
		if got := w.postedAbout("ada"); !slices.Equal(got, want) {
			t.Errorf("the Events about ada = %v, want %v", got, want)
		}
		failed := w.api.recorded.About(personKind, "ada")[1]
		if !strings.HasSuffix(failed.Message, "; the last good picture stays") {
			t.Errorf("the FetchFailed message = %q, want it to say the last good picture stays", failed.Message)
		}
		if failed.Metadata.Namespace != "default" {
			t.Errorf("the Event is in %q, want default", failed.Metadata.Namespace)
		}
	})
}

// A baker pod that passes its deadline posts BakeFailed, with the
// deadline in the message.
func TestABakerPodPastItsDeadlinePostsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada("nfs://nas.example/people/ada.png"))
		w.settle()

		time.Sleep(bakeDeadline)
		w.settle()

		got := w.api.recorded.About(personKind, "ada")
		if len(got) != 1 || got[0].Type != events.TypeWarning || got[0].Reason != reasonBakeFailed {
			t.Fatalf("the Events about ada = %+v, want one BakeFailed Warning", got)
		}
		if !strings.Contains(got[0].Message, "did not finish within 2m0s") {
			t.Errorf("the BakeFailed message = %q, want the deadline", got[0].Message)
		}
	})
}

// A refused status write posts nothing. The Event follows the write
// that lands.
func TestARefusedWritePostsNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.refuseStatusWrites(1)
		w.api.putPerson(ada(""))
		w.settle()
		if got := w.postedAbout("ada"); len(got) != 0 {
			t.Fatalf("a refused write posted %v", got)
		}

		time.Sleep(retryDelay)
		w.settle()

		want := []posted{{events.TypeNormal, reasonInitials, 1}}
		if got := w.postedAbout("ada"); !slices.Equal(got, want) {
			t.Errorf("the Events about ada = %v, want %v", got, want)
		}
	})
}
