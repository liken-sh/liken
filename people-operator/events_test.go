package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// posted is the part of an Event that a test compares.
type posted struct {
	kind, reason string
	count        int32
}

// postedAbout returns the Events about one Person, oldest first.
func (w *world) postedAbout(name string) []posted {
	var out []posted
	for _, event := range w.api.recorded.About(personKind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Count})
	}
	return out
}

// A transition of AvatarReady posts one Event, and it is a Warning for
// a failed read. A new picture under the same reason posts
// AvatarUpdated.
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
				w.waitForIdle()

				second := c.second
				if second.status == 0 {
					second.body = solid(t, 64, 64, blue)
				}
				w.pictures.serve("/ada.png", second)
				time.Sleep(recheckInterval)
				w.waitForIdle()

				if got := w.postedAbout("ada"); !slices.Equal(got, c.want) {
					t.Errorf("the Events about ada = %v, want %v", got, c.want)
				}
			})
		})
	}
}

// A failed read's Event says that status.thumbnail keeps the last good
// picture, and a later successful read posts Fetched again. The
// recorder merges a repeat of an Event within 10 minutes into the first
// one, so the first Fetched Event has a count of 2.
func TestARecoveredSourcePostsItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.waitForIdle()
		w.pictures.serve("/ada.png", servedPicture{status: http.StatusInternalServerError})
		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "again"))
		w.waitForIdle()
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "and again"))
		w.waitForIdle()

		want := []posted{
			{events.TypeNormal, reasonFetched, 2},
			{events.TypeWarning, reasonFetchFailed, 1},
		}
		if got := w.postedAbout("ada"); !slices.Equal(got, want) {
			t.Errorf("the Events about ada = %v, want %v", got, want)
		}
		failed := w.api.recorded.About(personKind, "ada")[1]
		if !strings.Contains(failed.Message, "keeps the last good picture") {
			t.Errorf("the FetchFailed message = %q, want it to say that status.thumbnail keeps the last good picture", failed.Message)
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
		w.waitForIdle()

		time.Sleep(bakeDeadline)
		w.waitForIdle()

		got := w.api.recorded.About(personKind, "ada")
		if len(got) != 1 || got[0].Type != events.TypeWarning || got[0].Reason != reasonBakeFailed {
			t.Fatalf("the Events about ada = %+v, want one BakeFailed Warning", got)
		}
		if !strings.Contains(got[0].Message, "did not finish within 2m0s") {
			t.Errorf("the BakeFailed message = %q, want the deadline", got[0].Message)
		}
	})
}

// A status write that the API server rejects posts nothing. The Event
// is posted after the write that succeeds.
func TestARefusedWritePostsNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.refuseStatusWrites(1)
		w.api.putPerson(ada(""))
		w.waitForIdle()
		if got := w.postedAbout("ada"); len(got) != 0 {
			t.Fatalf("the Events about ada after a refused write = %v, want none", got)
		}

		time.Sleep(retryDelay)
		w.waitForIdle()

		want := []posted{{events.TypeNormal, reasonInitials, 1}}
		if got := w.postedAbout("ada"); !slices.Equal(got, want) {
			t.Errorf("the Events about ada = %v, want %v", got, want)
		}
	})
}
