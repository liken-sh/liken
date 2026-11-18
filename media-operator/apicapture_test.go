package main

// These tests cover behavior that the route table tests do not: which
// sink an audio redirect points to and how it links the others, what
// the 409 detail says, and which query keys each upstream URL carries.

import (
	"net/http"
	"testing"
	"testing/synctest"
)

// An audio redirect points to the first sink in its Location and
// lists the other sinks in Link headers, so a client learns about them
// without reading the Player.
func TestAnAudioRedirectLinksTheOtherSinks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.audio = testAudioBase
		fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

		recorder := fixture.get(playerPathFor("audio.opus"))

		mustMatch(t, recorder.Code, http.StatusTemporaryRedirect)
		mustMatch(t, recorder.Header().Get("Location"),
			testAudioBase+"/v1/audio/sinks/hdmi-0-pch/audio.opus")
		mustContain(t, recorder.Header().Values("Link"),
			`<`+testAudioBase+`/v1/audio/sinks/aa-bb-cc-dd-ee-ff/audio.opus>; rel="https://liken.sh/rel/audio"`)
	})
}

// The detail of a 409 says what to do. A Player with no running Play
// needs a Play, and a Player that plays but has no resolved Sink needs
// spec.sinks.
func TestTheDetailOfA409NamesTheAction(t *testing.T) {
	rows := []struct {
		name    string
		playing bool
		sinks   int
		detail  string
	}{
		{"no Play runs", false, 2, "run a Play on this Player to open its sound"},
		{"a Play runs and no sink resolved", true, 0, "this Player resolves no Sink. Set spec.sinks on it and run a Play"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAPIFixture(t)
				fixture.plane.players[testAPIPlayer] = shapedPlayer(true, row.sinks, row.playing)

				recorder := fixture.get(playerPathFor("audio.wav"))

				mustMatch(t, recorder.Code, http.StatusConflict)
				mustMatch(t, problemOf(t, recorder).Detail, row.detail)
			})
		})
	}
}

// The display URL gets only the screen keys and the audio URL gets
// only t= and bitrate=. Both add the lead-in to t=.
func TestEachUpstreamURLGetsOnlyItsAspectKeys(t *testing.T) {
	query, err := parseCaptureQuery("t=0,10&xywh=0,0,960,540&width=480&framerate=15&quality=85&bitrate=128",
		mediaCaptureKeys)
	mustSucceed(t, err)

	mustMatch(t, displayTarget(testDisplayBase, "boe-1080", "mp4", query, composeLeadIn),
		testDisplayBase+"/v1/display/displays/boe-1080/screen.mp4"+
			"?t=1,11&xywh=0,0,960,540&width=480&framerate=15&quality=85")
	mustMatch(t, audioTarget(testAudioBase, "hdmi-0-pch", "opus", query, composeLeadIn),
		testAudioBase+"/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11&bitrate=128")
}

// The health paths follow the same method rules as the other routes:
// POST gets 405 with an Allow header, and OPTIONS gets 204.
func TestTheHealthPathsRefuseOtherMethods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		refused := fixture.call(http.MethodPost, healthTemplate, nil)
		options := fixture.call(http.MethodOptions, readyTemplate, nil)

		mustMatch(t, refused.Code, http.StatusMethodNotAllowed)
		mustMatch(t, refused.Header().Get("Allow"), apiAllowedMethods)
		mustMatch(t, options.Code, http.StatusNoContent)
	})
}
