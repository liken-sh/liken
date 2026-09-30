package main

// These tests run the operator's loop against the fake API server and
// the picture server, and read what the operator wrote to each
// Person's status.

import (
	"encoding/base64"
	"image/color"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Each source gives the Person a thumbnail and the reason it came from.
func TestEachSourceGivesAThumbnail(t *testing.T) {
	cases := []struct {
		name       string
		avatar     string
		wantReason string
		wantColour func(p *person) bool
	}{
		{"no source", "", reasonInitials, func(p *person) bool { return isInitials(p.Status.Thumbnail) }},
		{"an https source", "https://pictures.example/ada.png", reasonFetched, func(p *person) bool { return !isInitials(p.Status.Thumbnail) }},
		{"an http source", "http://pictures.example/ada.png", reasonFetched, func(p *person) bool { return !isInitials(p.Status.Thumbnail) }},
		{"a data: source", "data:image/png;base64,", reasonInline, func(p *person) bool { return !isInitials(p.Status.Thumbnail) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				picture := solid(t, 64, 64, green)
				w.pictures.serve("/ada.png", servedPicture{body: picture, etag: `"one"`})
				avatar := c.avatar
				if avatar == "data:image/png;base64," {
					avatar += base64.StdEncoding.EncodeToString(picture)
				}
				w.api.putPerson(ada(avatar))
				w.settle()

				got := w.api.person(t, "ada")
				ready := got.Status.ready()
				if ready.Status != "True" || ready.Reason != c.wantReason || ready.ObservedGeneration != 1 {
					t.Errorf("AvatarReady = %+v, want True, %s, generation 1", ready, c.wantReason)
				}
				if !c.wantColour(&got) {
					t.Errorf("the thumbnail is not the picture the source names")
				}
				if got.Status.Avatar.Source != avatar {
					t.Errorf("status.avatar.source = %q, want %q", got.Status.Avatar.Source, avatar)
				}
				if bounds := decodeThumbnail(t, got.Status.Thumbnail).Bounds(); bounds.Dx() != 256 || bounds.Dy() != 256 {
					t.Errorf("the thumbnail is %v, want 256x256", bounds)
				}
			})
		})
	}
}

// A fetched picture's ETag goes into the status, so the slow check can
// ask whether it changed.
func TestAFetchRecordsTheETag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.settle()

		if got := w.api.person(t, "ada").Status.Avatar.ETag; got != `"one"` {
			t.Errorf("status.avatar.etag = %q, want %q", got, `"one"`)
		}
	})
}

// Every six hours the operator asks each HTTP source whether its
// picture changed. A 304 changes nothing, and a new picture replaces
// the thumbnail.
func TestTheSlowCheckAsksWhetherThePictureChanged(t *testing.T) {
	cases := []struct {
		name        string
		etag        string
		colour      color.RGBA
		wantWritten bool
	}{
		{"the same picture", `"one"`, green, false},
		{"a new picture", `"two"`, blue, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
				w.api.putPerson(ada("https://pictures.example/ada.png"))
				w.settle()
				before := w.api.person(t, "ada")

				w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, c.colour), etag: c.etag})
				time.Sleep(recheckInterval)
				w.settle()

				after := w.api.person(t, "ada")
				if got := w.pictures.requests(); !slices.Equal(got, []string{"", `"one"`}) {
					t.Errorf("If-None-Match of each request = %q, want none and then %q", got, `"one"`)
				}
				if after.Status.Avatar.ETag != c.etag {
					t.Errorf("status.avatar.etag = %q, want %q", after.Status.Avatar.ETag, c.etag)
				}
				if !near(centre(t, after.Status.Thumbnail), c.colour) {
					t.Errorf("the thumbnail's centre is %v, want %v", centre(t, after.Status.Thumbnail), c.colour)
				}
				written := after.Metadata.ResourceVersion != before.Metadata.ResourceVersion
				if written != c.wantWritten {
					t.Errorf("the status was written: %v, want %v", written, c.wantWritten)
				}
			})
		})
	}
}

// A new value of the check-avatar annotation reads the picture again,
// and the status records the value it answered.
func TestTheAnnotationReadsThePictureAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.settle()

		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "2026-09-30T19:00:00Z"))
		w.settle()

		if got := len(w.pictures.requests()); got != 2 {
			t.Errorf("the picture server took %d requests, want 2", got)
		}
		if got := w.api.person(t, "ada").Status.Avatar.Checked; got != "2026-09-30T19:00:00Z" {
			t.Errorf("status.avatar.checked = %q, want the annotation's value", got)
		}
	})
}

// A source that fails after it worked keeps the last good picture, and
// the condition says why. A source that never worked shows the
// initials.
func TestAFailedReadKeepsTheLastGoodPicture(t *testing.T) {
	cases := []struct {
		name         string
		first        servedPicture
		wantInitials bool
	}{
		{"a source that worked", servedPicture{etag: `"one"`}, false},
		{"a source that never worked", servedPicture{status: http.StatusNotFound}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				first := c.first
				if first.status == 0 {
					first.body = solid(t, 64, 64, green)
				}
				w.pictures.serve("/ada.png", first)
				w.api.putPerson(ada("https://pictures.example/ada.png"))
				w.settle()

				w.pictures.serve("/ada.png", servedPicture{status: http.StatusInternalServerError})
				w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "again"))
				w.settle()

				got := w.api.person(t, "ada")
				ready := got.Status.ready()
				if ready.Status != "False" || ready.Reason != reasonFetchFailed {
					t.Errorf("AvatarReady = %+v, want False, FetchFailed", ready)
				}
				if isInitials(got.Status.Thumbnail) != c.wantInitials {
					t.Errorf("the thumbnail is initials: %v, want %v", !c.wantInitials, c.wantInitials)
				}
			})
		})
	}
}

// A scheme the operator does not read shows the initials, and the
// condition names the scheme.
func TestAnUnsupportedSchemeShowsTheInitials(t *testing.T) {
	cases := []string{"ftp://pictures.example/ada.png", "ada.png", "https://pictures.example/%zz"}
	for _, avatar := range cases {
		t.Run(avatar, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada(avatar))
				w.settle()

				got := w.api.person(t, "ada")
				if ready := got.Status.ready(); ready.Status != "False" || ready.Reason != reasonUnsupportedScheme {
					t.Errorf("AvatarReady = %+v, want False, UnsupportedScheme", ready)
				}
				if !isInitials(got.Status.Thumbnail) {
					t.Error("the thumbnail is not the initials")
				}
			})
		})
	}
}

// A new name redraws the initials.
func TestANewNameRedrawsTheInitials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada(""))
		w.settle()
		before := w.api.person(t, "ada").Status.Thumbnail

		renamed := ada("")
		renamed.Spec.Nickname = "Countess"
		w.api.putPerson(renamed)
		w.settle()

		after := w.api.person(t, "ada")
		if after.Status.Thumbnail == before {
			t.Error("the initials stayed the same after the name changed")
		}
		if got := after.Status.ready().ObservedGeneration; got != 2 {
			t.Errorf("observedGeneration = %d, want 2", got)
		}
	})
}

// A status that already answers the spec is not written again, so a
// pass that finds nothing to do writes nothing.
func TestAPassWritesOnlyAChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada(""))
		w.settle()
		before := w.api.person(t, "ada").Metadata.ResourceVersion

		time.Sleep(recheckInterval)
		w.settle()

		if after := w.api.person(t, "ada").Metadata.ResourceVersion; after != before {
			t.Errorf("the status was written again: version %s, then %s", before, after)
		}
	})
}

// A status write that fails is sent again after the retry delay, with
// no event to wake the pass.
func TestAFailedWriteIsSentAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.refuseStatusWrites(1)
		w.api.putPerson(ada(""))
		w.settle()
		if got := w.api.person(t, "ada").Status.Thumbnail; got != "" {
			t.Fatal("the refused write landed")
		}

		time.Sleep(retryDelay)
		w.settle()

		if !isInitials(w.api.person(t, "ada").Status.Thumbnail) {
			t.Error("the write was not sent again")
		}
	})
}
