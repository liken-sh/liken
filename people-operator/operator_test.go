package main

// These tests run the operator's loop against a fake API server and a
// picture server, then read the status that the operator wrote to each
// Person.

import (
	"encoding/base64"
	"image/color"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Each kind of source produces a thumbnail and the AvatarReady reason for it.
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
				w.waitForIdle()

				got := w.api.person(t, "ada")
				ready := got.Status.ready()
				if ready.Status != "True" || ready.Reason != c.wantReason || ready.ObservedGeneration != 1 {
					t.Errorf("AvatarReady = %+v, want True, %s, generation 1", ready, c.wantReason)
				}
				if !c.wantColour(&got) {
					t.Error("the thumbnail does not have the colours of the picture at the source, want them")
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

// The operator records the ETag of a fetched picture in the status, so
// that the recheck can send a conditional request.
func TestAFetchRecordsTheETag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.waitForIdle()

		if got := w.api.person(t, "ada").Status.Avatar.ETag; got != `"one"` {
			t.Errorf("status.avatar.etag = %q, want %q", got, `"one"`)
		}
	})
}

// Each recheckInterval the operator sends an HTTP source a request with
// If-None-Match. A 304 leaves the status unchanged, and a new picture
// replaces the thumbnail.
func TestRecheckReplacesTheThumbnailOnlyForANewPicture(t *testing.T) {
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
				w.waitForIdle()
				before := w.api.person(t, "ada")

				w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, c.colour), etag: c.etag})
				time.Sleep(recheckInterval)
				w.waitForIdle()

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

// A new value of the check-avatar annotation makes the operator read
// the picture again and record the value in status.avatar.checked.
func TestAnnotationChangeRereadsThePicture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.pictures.serve("/ada.png", servedPicture{body: solid(t, 64, 64, green), etag: `"one"`})
		w.api.putPerson(ada("https://pictures.example/ada.png"))
		w.waitForIdle()

		w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "2026-09-30T19:00:00Z"))
		w.waitForIdle()

		if got := len(w.pictures.requests()); got != 2 {
			t.Errorf("the picture server received %d requests, want 2", got)
		}
		if got := w.api.person(t, "ada").Status.Avatar.Checked; got != "2026-09-30T19:00:00Z" {
			t.Errorf("status.avatar.checked = %q, want the annotation's value", got)
		}
	})
}

// When a source fails after it has worked, the status keeps the last
// good thumbnail and the condition gives the reason. When a source has
// never worked, the thumbnail shows the initials.
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
				w.waitForIdle()

				w.pictures.serve("/ada.png", servedPicture{status: http.StatusInternalServerError})
				w.api.putPerson(withAnnotation(ada("https://pictures.example/ada.png"), "again"))
				w.waitForIdle()

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

// A spec.avatar that is not a URI, or whose scheme the operator does
// not read, gets the initials and the reason UnsupportedScheme.
func TestUnsupportedSchemeGetsTheInitials(t *testing.T) {
	cases := []string{"ftp://pictures.example/ada.png", "ada.png", "https://pictures.example/%zz"}
	for _, avatar := range cases {
		t.Run(avatar, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada(avatar))
				w.waitForIdle()

				got := w.api.person(t, "ada")
				if ready := got.Status.ready(); ready.Status != "False" || ready.Reason != reasonUnsupportedScheme {
					t.Errorf("AvatarReady = %+v, want False, UnsupportedScheme", ready)
				}
				if !isInitials(got.Status.Thumbnail) {
					t.Error("the thumbnail is not the initials, want the initials")
				}
			})
		})
	}
}

// A changed name redraws the initials.
func TestANewNameRedrawsTheInitials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada(""))
		w.waitForIdle()
		before := w.api.person(t, "ada").Status.Thumbnail

		renamed := ada("")
		renamed.Spec.Nickname = "Countess"
		w.api.putPerson(renamed)
		w.waitForIdle()

		after := w.api.person(t, "ada")
		if after.Status.Thumbnail == before {
			t.Error("the initials are unchanged after the nickname changed, want new initials")
		}
		if got := after.Status.ready().ObservedGeneration; got != 2 {
			t.Errorf("observedGeneration = %d, want 2", got)
		}
	})
}

// A pass does not write a status that already matches the spec.
func TestPassDoesNotRewriteAnUnchangedStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada(""))
		w.waitForIdle()
		before := w.api.person(t, "ada").Metadata.ResourceVersion

		time.Sleep(recheckInterval)
		w.waitForIdle()

		if after := w.api.person(t, "ada").Metadata.ResourceVersion; after != before {
			t.Errorf("the Person changed from resourceVersion %s to %s, want no status write", before, after)
		}
	})
}

// After a failed status write, the operator retries after retryDelay
// with no watch event.
func TestFailedStatusWriteIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.refuseStatusWrites(1)
		w.api.putPerson(ada(""))
		w.waitForIdle()
		if got := w.api.person(t, "ada").Status.Thumbnail; got != "" {
			t.Fatal("the status has a thumbnail after the refused write, want none")
		}

		time.Sleep(retryDelay)
		w.waitForIdle()

		if !isInitials(w.api.person(t, "ada").Status.Thumbnail) {
			t.Error("the status has no initials after the retry, want the initials")
		}
	})
}
