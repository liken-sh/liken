package main

// These tests cover the Event a capture leaves on the Player: which
// object it names and which caller it names, that a repeat adds to the
// count of one Event, and that an Event the API server refuses never
// fails the capture. Each runs in a synctest bubble, so the recorder's
// queue and its retries run on the bubble's clock.

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// An Event the API server refuses is a line in the recorder's log and
// never a failed capture: the client still gets its 200 and its bytes.
func TestACaptureOutlivesAnEventTheApiServerRefuses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
		fixture.events.Refuse(3)

		recorder := fixture.get(playerPathFor("media.mp4"))
		time.Sleep(time.Minute)
		synctest.Wait()

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, len(fixture.events.List()), 0)
	})
}

// The Event names the Player it was taken from, by namespace, name,
// and UID, and the caller who took it.
func TestTheCapturedEventNamesThePlayerAndTheCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

		fixture.get(playerPathFor("media.mp4"))
		synctest.Wait()

		posted := fixture.events.About("Player", testAPIPlayer)
		mustMatch(t, len(posted), 1)
		event := posted[0]
		mustMatch(t, event.InvolvedObject.Namespace, testAPINamespace)
		mustMatch(t, event.InvolvedObject.UID, "player-uid")
		mustMatch(t, event.InvolvedObject.APIVersion, mediaAPIVersion)
		mustMatch(t, event.Metadata.Namespace, testAPINamespace)
		mustMatch(t, event.ReportingComponent, apiComponent)
		mustMatch(t, event.Type, "Normal")
		mustMatch(t, event.Reason, reasonCaptured)
		mustMatch(t, event.Count, int32(1))
		mustMatch(t, event.Message,
			testAPISubject+" took the media of "+testAPIPlayer+" as video/mp4")
	})
}

// The same caller who takes the same capture again within ten minutes
// adds to the count of one Event, so `kubectl describe player` prints
// one line for the repeats.
func TestARepeatedCaptureCountsOnOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

		fixture.get(playerPathFor("media.mp4"))
		synctest.Wait()
		time.Sleep(time.Minute)
		fixture.get(playerPathFor("media.mp4"))
		synctest.Wait()

		posted := fixture.events.About("Player", testAPIPlayer)
		mustMatch(t, len(posted), 1)
		mustMatch(t, posted[0].Count, int32(2))
	})
}
