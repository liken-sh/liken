package main

// The limits on one fetch, against the in-memory picture server. Each
// test runs in a synctest bubble, so the 15-second timeout passes on
// the fake clock.

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestTheFetchLimits(t *testing.T) {
	cases := []struct {
		name       string
		served     servedPicture
		wantReason string
	}{
		{"a server that never answers", servedPicture{hold: true}, reasonFetchFailed},
		{"a body over 10 MiB", servedPicture{body: make([]byte, maxPictureBytes+1)}, reasonFetchFailed},
		{"a picture over 8192 pixels on a side", servedPicture{}, reasonDecodeFailed},
		{"a file in no format the operator reads", servedPicture{body: []byte("<html>")}, reasonDecodeFailed},
		{"an answer that is not 200", servedPicture{status: http.StatusForbidden}, reasonFetchFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pictures := startPictureServer(t)
				served := c.served
				if served.body == nil && !served.hold && served.status == 0 {
					served.body = solid(t, maxPictureSide+1, 1, green)
				}
				pictures.serve("/ada.png", served)

				_, err := newHTTPSource(pictures.server).read(t.Context(), mustParse(t, "https://pictures.example/ada.png"), pictureVersion{}, blue)
				if got := reasonOf(err); err == nil || got != c.wantReason {
					t.Errorf("error = %v with reason %s, want reason %s", err, got, c.wantReason)
				}
			})
		})
	}
}

// A server that cannot be reached is a failed fetch.
func TestAFetchFromNoServerFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pictures := startPictureServer(t)
		pictures.server.SetDown(true)

		_, err := newHTTPSource(pictures.server).read(t.Context(), mustParse(t, "https://pictures.example/ada.png"), pictureVersion{}, blue)
		if reasonOf(err) != reasonFetchFailed {
			t.Errorf("error = %v, want reason FetchFailed", err)
		}
	})
}

// The fetch sends the recorded Last-Modified as If-Modified-Since, and
// records the answer's Last-Modified.
func TestAFetchSendsIfModifiedSince(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sent string
		server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent = r.Header.Get("If-Modified-Since")
			w.Header().Set("Last-Modified", "Wed, 30 Sep 2026 12:00:00 GMT")
			_, _ = w.Write(solid(t, 8, 8, green))
		})
		pictures := apiservertest.Start(t, server)

		got, err := newHTTPSource(pictures).read(t.Context(), mustParse(t, "https://pictures.example/ada.png"),
			pictureVersion{LastModified: "Tue, 29 Sep 2026 14:02:11 GMT"}, blue)
		if err != nil {
			t.Fatal(err)
		}
		if sent != "Tue, 29 Sep 2026 14:02:11 GMT" {
			t.Errorf("If-Modified-Since = %q, want the recorded time", sent)
		}
		if got.version.LastModified != "Wed, 30 Sep 2026 12:00:00 GMT" {
			t.Errorf("the version's Last-Modified = %q, want the answer's", got.version.LastModified)
		}
	})
}
