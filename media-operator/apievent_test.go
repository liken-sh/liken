package main

// These tests cover the Event a capture leaves on the Player: which
// object it names and which request it ties to, and that an Event the
// API server refuses never fails the capture.

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// An Event the API server refuses is a line on stderr and never a
// failed capture: the client still gets its 200 and its bytes.
func TestACaptureOutlivesAnEventTheApiServerRefuses(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
	fixture.plane.refuseEvents = true

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, len(fixture.plane.events), 0)
}

// The Event names the Player it was taken from, by namespace, name,
// and UID, and the request that took it, by the request id in its
// name.
func TestTheCapturedEventNamesThePlayerAndTheRequest(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

	fixture.get(playerPathFor("media.mp4"))

	event := fixture.plane.events[0]
	mustMatch(t, event.InvolvedObject.Namespace, testAPINamespace)
	mustMatch(t, event.InvolvedObject.UID, "player-uid")
	mustMatch(t, event.InvolvedObject.APIVersion, mediaAPIVersion)
	mustMatch(t, event.Metadata.Namespace, testAPINamespace)
	mustMatch(t, event.Count, 1)
	mustMatch(t, event.Metadata.Name, testAPIPlayer+"."+fixture.lines[0].ID)
	mustMatch(t, event.Message,
		testAPISubject+" took the media of "+testAPIPlayer+" as video/mp4")
}
