package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestACaptureSucceedsWhenTheApiServerRefusesTheEvent(t *testing.T) {
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
