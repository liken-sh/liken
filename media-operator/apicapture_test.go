package main

// These tests cover what the route table alone does not: which sink
// an audio redirect names and how it links the rest, what the 409
// tells a unit with nothing to tap, and which query keys each upstream
// request carries.

import (
	"net/http"
	"testing"
)

// An audio redirect names the first sink in its Location and links the
// rest, so the response says the other sinks exist without a read of
// the Player.
func TestAnAudioRedirectLinksTheOtherSinks(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.audio = testAudioBase
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

	recorder := fixture.get(playerPathFor("audio.opus"))

	mustMatch(t, recorder.Code, http.StatusTemporaryRedirect)
	mustMatch(t, recorder.Header().Get("Location"),
		testAudioBase+"/v1/audio/sinks/hdmi-0-pch/audio.opus")
	mustContain(t, recorder.Header().Values("Link"),
		`<`+testAudioBase+`/v1/audio/sinks/aa-bb-cc-dd-ee-ff/audio.opus>; rel="https://liken.sh/rel/audio"`)
}

// The detail of a 409 names the action that clears it, and the action
// differs: a unit with no running Play needs a Play, and a unit that
// plays but resolved no Sink needs spec.sinks.
func TestTheDetailOfA409NamesTheAction(t *testing.T) {
	rows := []struct {
		name    string
		playing bool
		sinks   int
		detail  string
	}{
		{"no Play runs", false, 2, "no Play runs on this Player; run a Play to open its sound"},
		{"a Play runs and no sink resolved", true, 0, "this Player resolves no Sink; state spec.sinks and run a Play"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := newAPIFixture(t)
			fixture.plane.players[testAPIPlayer] = shapedPlayer(true, row.sinks, row.playing)

			recorder := fixture.get(playerPathFor("audio.wav"))

			mustMatch(t, recorder.Code, http.StatusConflict)
			mustMatch(t, problemOf(t, recorder).Detail, row.detail)
		})
	}
}

// Each upstream carries the knobs its own aspect takes and no others:
// the screen knobs go to the display API and bitrate to the audio
// API, each with the lead-in added to t=.
func TestEachUpstreamCarriesItsOwnKnobs(t *testing.T) {
	query, err := parseCaptureQuery("t=0,10&xywh=0,0,960,540&width=480&framerate=15&quality=85&bitrate=128",
		mediaCaptureKeys)
	mustSucceed(t, err)

	mustMatch(t, displayTarget(testDisplayBase, "boe-1080", "mp4", query, composeLeadIn),
		testDisplayBase+"/v1/display/displays/boe-1080/screen.mp4"+
			"?t=1,11&xywh=0,0,960,540&width=480&framerate=15&quality=85")
	mustMatch(t, audioTarget(testAudioBase, "hdmi-0-pch", "opus", query, composeLeadIn),
		testAudioBase+"/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11&bitrate=128")
}

// The health paths obey the same method rules as every other route: a
// POST is 405 with Allow, and an OPTIONS is 204.
func TestTheHealthPathsRefuseOtherMethods(t *testing.T) {
	fixture := newAPIFixture(t)

	refused := fixture.call(http.MethodPost, healthTemplate, nil)
	options := fixture.call(http.MethodOptions, readyTemplate, nil)

	mustMatch(t, refused.Code, http.StatusMethodNotAllowed)
	mustMatch(t, refused.Header().Get("Allow"), apiAllowedMethods)
	mustMatch(t, options.Code, http.StatusNoContent)
}
