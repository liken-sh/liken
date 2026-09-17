package main

// These tests cover the router against the plan: every row of the
// route table, every row of the negotiation table, the stream count
// rule from zero to three streams, and a redirect's Location and Link
// read byte for byte.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheRouteTableAnswersEveryRow(t *testing.T) {
	rows := []struct {
		name   string
		method string
		target string
		status int
	}{
		{"discovery", http.MethodGet, apiBasePath, http.StatusOK},
		{"openapi", http.MethodGet, apiBasePath + "/openapi.json", http.StatusOK},
		{"info", http.MethodGet, playerPathFor(""), http.StatusOK},
		{"screen", http.MethodGet, playerPathFor("screen"), http.StatusTemporaryRedirect},
		{"screen.png", http.MethodGet, playerPathFor("screen.png"), http.StatusTemporaryRedirect},
		{"screen.mp4", http.MethodGet, playerPathFor("screen.mp4"), http.StatusTemporaryRedirect},
		{"audio", http.MethodGet, playerPathFor("audio"), http.StatusTemporaryRedirect},
		{"audio.wav", http.MethodGet, playerPathFor("audio.wav"), http.StatusTemporaryRedirect},
		{"media head", http.MethodHead, playerPathFor("media.mp4"), http.StatusOK},
		{"options", http.MethodOptions, playerPathFor("media.mp4"), http.StatusNoContent},
		{"other method", http.MethodPost, playerPathFor("media.mp4"), http.StatusMethodNotAllowed},
		{"no route", http.MethodGet, "/v1/media/nothing", http.StatusNotFound},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := newAPIFixture(t)

			recorder := fixture.call(row.method, row.target, nil)

			mustMatch(t, recorder.Code, row.status)
			mustMatch(t, recorder.Header().Get("Vary"), "Accept")
		})
	}
}

func TestEveryResponseCarriesTheServiceLinks(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.get(playerPathFor("screen.png"))

	links := recorder.Header().Values("Link")
	mustContain(t, links, `</v1/media/openapi.json>; rel="service-desc"`)
	mustContain(t, links, `<https://media.liken.sh/docs/reference/api/>; rel="service-doc"`)
	mustContain(t, links,
		`<https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio>; rel="describedby"`)
}

func TestOptionsAnswersTheMethodsAndNoBody(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.call(http.MethodOptions, playerPathFor("screen"), nil)

	mustMatch(t, recorder.Code, http.StatusNoContent)
	mustMatch(t, recorder.Header().Get("Allow"), apiAllowedMethods)
	mustMatch(t, recorder.Body.Len(), 0)
}

func TestARefusedMethodCarriesAllowAndAProblem(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.call(http.MethodPut, playerPathFor("media.mp4"), nil)

	mustMatch(t, recorder.Code, http.StatusMethodNotAllowed)
	mustMatch(t, recorder.Header().Get("Allow"), apiAllowedMethods)
	mustMatch(t, recorder.Header().Get("Content-Type"), problemContentType)
}

func TestNoTokenIsRefusedWithTheRealmAlone(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.call(http.MethodGet, playerPathFor("screen"),
		http.Header{"Authorization": {""}})

	mustMatch(t, recorder.Code, http.StatusUnauthorized)
	mustMatch(t, recorder.Header().Get("WWW-Authenticate"), `Bearer realm="media-api"`)
}

func TestARefusedTokenCarriesTheReviewsOwnWords(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.authenticated = false
	fixture.plane.words = "token is expired"

	recorder := fixture.get(playerPathFor("screen"))

	mustMatch(t, recorder.Code, http.StatusUnauthorized)
	mustMatch(t, recorder.Header().Get("WWW-Authenticate"),
		`Bearer realm="media-api", error="invalid_token", error_description="token is expired"`)
	mustMatch(t, problemOf(t, recorder).Detail, "token is expired")
}

func TestADenialNamesTheScopeItRefused(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.allowed = false

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusForbidden)
	mustMatch(t, recorder.Header().Get("WWW-Authenticate"),
		`Bearer realm="media-api", error="insufficient_scope", scope="players/media"`)
}

// The SubjectAccessReview names the path's namespace and the aspect's
// subresource, with the subject the TokenReview answered.
func TestTheReviewNamesThePathsNamespaceAndTheAspect(t *testing.T) {
	fixture := newAPIFixture(t)

	fixture.get(playerPathFor("audio.wav"))

	review := fixture.plane.reviews[0]
	mustMatch(t, review.Spec.ResourceAttributes.Namespace, testAPINamespace)
	mustMatch(t, review.Spec.ResourceAttributes.Resource, "players")
	mustMatch(t, review.Spec.ResourceAttributes.Subresource, "audio")
	mustMatch(t, review.Spec.ResourceAttributes.Verb, "get")
	mustMatch(t, review.Spec.User, testAPISubject)
}

// A 403 answers before the Player is read, so a denied caller is
// told nothing about whether the name exists.
func TestADenialNeverReadsThePlayer(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.allowed = false
	delete(fixture.plane.players, testAPIPlayer)

	recorder := fixture.get(playerPathFor("screen"))

	mustMatch(t, recorder.Code, http.StatusForbidden)
}

func TestAPlayerThatIsNotThereIsANotFound(t *testing.T) {
	fixture := newAPIFixture(t)
	delete(fixture.plane.players, testAPIPlayer)

	recorder := fixture.get(playerPathFor("screen"))

	mustMatch(t, recorder.Code, http.StatusNotFound)
}

// The plan's negotiation table, row by row: the negotiated media route
// and its Content-Location, a refused extension, and the redirects
// each Accept produces.
func TestNegotiationAnswersEveryRowOfThePlan(t *testing.T) {
	rows := []struct {
		name            string
		method          string
		target          string
		accept          string
		status          int
		contentType     string
		contentLocation string
		location        string
	}{
		{
			name: "media with no Accept", method: http.MethodHead, target: playerPathFor("media"),
			status: http.StatusOK, contentType: "video/mp4",
			contentLocation: playerPathFor("media.mp4"),
		},
		{
			name: "media as matroska", method: http.MethodHead, target: playerPathFor("media"), accept: "video/matroska",
			status: http.StatusOK, contentType: "video/matroska",
			contentLocation: playerPathFor("media.mkv"),
		},
		{
			name: "matroska over a wildcard", method: http.MethodHead, target: playerPathFor("media"),
			accept: "video/*;q=0.5, video/matroska",
			status: http.StatusOK, contentType: "video/matroska",
			contentLocation: playerPathFor("media.mkv"),
		},
		{
			name: "an extension the Accept excludes", target: playerPathFor("media.mp4"),
			accept: "image/png", status: http.StatusNotAcceptable,
		},
		{
			name: "screen as jpeg", target: playerPathFor("screen"), accept: "image/jpeg",
			status:   http.StatusTemporaryRedirect,
			location: testDisplayBase + "/v1/display/displays/boe-1080/screen.jpg",
		},
		{
			name: "screen with no Accept", target: playerPathFor("screen"),
			status:   http.StatusTemporaryRedirect,
			location: testDisplayBase + "/v1/display/displays/boe-1080/screen.png",
		},
		{
			name: "audio as ogg", target: playerPathFor("audio"), accept: "audio/ogg",
			status:   http.StatusTemporaryRedirect,
			location: testAudioBase + "/v1/audio/sinks/hdmi-0-pch/audio.opus",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := newAPIFixture(t)
			fixture.server.display = testDisplayBase
			fixture.server.audio = testAudioBase
			method := row.method
			if method == "" {
				method = http.MethodGet
			}

			recorder := fixture.call(method, row.target, http.Header{"Accept": {row.accept}})

			mustMatch(t, recorder.Code, row.status)
			if row.contentType != "" {
				mustMatch(t, strings.Split(recorder.Header().Get("Content-Type"), ";")[0], row.contentType)
			}
			mustMatch(t, recorder.Header().Get("Content-Location"), row.contentLocation)
			mustMatch(t, recorder.Header().Get("Location"), row.location)
		})
	}
}

// A 406 on an extension route lists the route's own type beside its
// siblings, each with the route that serves it.
func TestA406ListsEveryFormWithItsHref(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.accept(playerPathFor("media.mp4"), "image/png")

	document := problemOf(t, recorder)
	mustMatch(t, document.Type, problemNotAcceptable)
	mustMatch(t, len(document.Acceptable), 2)
	mustMatch(t, document.Acceptable[0], acceptableForm{Type: "video/mp4", Href: playerPathFor("media.mp4")})
	mustMatch(t, document.Acceptable[1], acceptableForm{Type: "video/matroska", Href: playerPathFor("media.mkv")})
}

// The redirect a client reads, field by field: the Location with the
// caller's query, no-store, the related link to the composed form at
// the same span, and no upstream call.
func TestARedirectReadsByteForByte(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.display = testDisplayBase

	recorder := fixture.get(playerPathFor("screen.mp4") + "?t=0,10&width=960")

	mustMatch(t, recorder.Code, http.StatusTemporaryRedirect)
	mustMatch(t, recorder.Header().Get("Location"),
		testDisplayBase+"/v1/display/displays/boe-1080/screen.mp4?t=0,10&width=960")
	mustMatch(t, recorder.Header().Get("Cache-Control"), "no-store")
	mustContain(t, recorder.Header().Values("Link"),
		`</v1/media/namespaces/media/players/studio/media.mp4?t=0,10>; rel="related"; type="video/mp4"`)
	mustMatch(t, len(fixture.display.made()), 0)
}

// A query the grammar refuses is a 400 before any upstream is
// reached, so it costs no capture.
func TestAQueryTheGrammarRefusesCostsNoCapture(t *testing.T) {
	rows := []string{"?t=10,10", "?t=5,3", "?t=1&t=2", "?t=61,70", "?width=10&height=10", "?zoom=2"}
	for _, row := range rows {
		t.Run(row, func(t *testing.T) {
			fixture := newAPIFixture(t)

			recorder := fixture.get(playerPathFor("media.mp4") + row)

			mustMatch(t, recorder.Code, http.StatusBadRequest)
			mustMatch(t, len(fixture.display.made()), 0)
		})
	}
}

// The three Media Fragments section 6.1.1 cases reach the upstream as
// their literal forms do.
func TestThePercentEncodedSpansReachTheUpstream(t *testing.T) {
	rows := []struct {
		query    string
		location string
	}{
		{"?t=10%2C20", "?t=10,20"},
		{"?t=%6ept:10", "?t=10"},
		{"?t=npt%3a10", "?t=10"},
	}
	for _, row := range rows {
		t.Run(row.query, func(t *testing.T) {
			fixture := newAPIFixture(t)
			fixture.server.display = testDisplayBase

			recorder := fixture.get(playerPathFor("screen.mp4") + row.query)

			mustMatch(t, recorder.Header().Get("Location"),
				testDisplayBase+"/v1/display/displays/boe-1080/screen.mp4"+row.location)
		})
	}
}

// The stream count rule from zero to three streams: zero is a 409,
// one redirects to that stream's own route, and more than one
// composes. Sinks with no running Play count for nothing.
func TestTheStreamCountRuleAnswersZeroToThreeStreams(t *testing.T) {
	rows := []struct {
		name     string
		screen   bool
		sinks    int
		playing  bool
		status   int
		location string
	}{
		{name: "nothing at all", status: http.StatusConflict},
		{name: "a screen with no sink", screen: true, playing: true,
			status:   http.StatusTemporaryRedirect,
			location: testDisplayBase + "/v1/display/displays/boe-1080/screen.mp4"},
		{name: "a screen with sinks but no Play", screen: true, sinks: 2,
			status:   http.StatusTemporaryRedirect,
			location: testDisplayBase + "/v1/display/displays/boe-1080/screen.mp4"},
		{name: "one sink and no screen", sinks: 1, playing: true,
			status:   http.StatusTemporaryRedirect,
			location: testAudioBase + "/v1/audio/sinks/hdmi-0-pch/audio.opus"},
		{name: "two sinks and no screen", sinks: 2, playing: true, status: http.StatusOK},
		{name: "a screen and one sink", screen: true, sinks: 1, playing: true, status: http.StatusOK},
		{name: "a screen and two sinks", screen: true, sinks: 2, playing: true, status: http.StatusOK},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := newAPIFixture(t)
			fixture.server.display = testDisplayBase
			fixture.server.audio = testAudioBase
			fixture.plane.players[testAPIPlayer] = shapedPlayer(row.screen, row.sinks, row.playing)

			recorder := fixture.call(http.MethodHead, playerPathFor("media.mp4"), nil)

			mustMatch(t, recorder.Code, row.status)
			mustMatch(t, recorder.Header().Get("Location"), row.location)
		})
	}
}

func shapedPlayer(screen bool, sinks int, playing bool) *Player {
	player := &Player{
		Metadata: ObjectMeta{Namespace: testAPINamespace, Name: testAPIPlayer, UID: "player-uid"},
	}
	if screen {
		player.Status.Screen = &PlayerScreenStatus{Node: "stick1", Monitor: testAPIMonitor}
	}
	if playing {
		player.Status.Activity = playerPlaying
		player.Status.Play = "movie"
	}
	names := []string{testAPISink, testAPISecond}
	for index := 0; index < sinks; index++ {
		player.Status.Sinks = append(player.Status.Sinks,
			PlayerSinkStatus{Request: audioRequestPrefix + string(rune('0'+index)), Name: names[index]})
	}
	return player
}

// The audio aspect of a unit with no running Play is a 409
// not-playing whose detail names the action.
func TestTheAudioAspectNeedsARunningPlay(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, false)

	recorder := fixture.get(playerPathFor("audio.wav"))

	mustMatch(t, recorder.Code, http.StatusConflict)
	document := problemOf(t, recorder)
	mustMatch(t, document.Type, problemNotPlaying)
	mustMatch(t, document.Detail, "no Play runs on this Player; run a Play to open its sound")
}

// A screen the Player never resolved is a 409 no-node, not a 404,
// because the Player exists and the caller can act.
func TestTheScreenAspectNeedsAResolvedScreen(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(false, 1, true)

	recorder := fixture.get(playerPathFor("screen.png"))

	mustMatch(t, recorder.Code, http.StatusConflict)
	mustMatch(t, problemOf(t, recorder).Type, problemNoNode)
}

// A HEAD takes no frame and asks no sibling for one, and its
// Content-Type omits the codecs parameter, which only a GET carries.
func TestAHeadMakesNoUpstreamCallAndOmitsTheCodecs(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

	recorder := fixture.call(http.MethodHead, playerPathFor("media.mp4"), nil)

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, recorder.Header().Get("Content-Type"), "video/mp4")
	mustMatch(t, recorder.Body.Len(), 0)
	mustMatch(t, len(fixture.display.made()), 0)
	mustMatch(t, len(fixture.audio.made()), 0)
}

// A composed answer carries the capture fields whether it streams or
// is a HEAD: no-store, no ranges, the file name, and one link per
// upstream with the lead-in added to t=.
func TestAComposedAnswerCarriesTheCaptureFields(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.display = testDisplayBase
	fixture.server.audio = testAudioBase
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

	recorder := fixture.call(http.MethodHead, playerPathFor("media.mp4")+"?t=0,10&width=960", nil)

	header := recorder.Header()
	mustMatch(t, header.Get("Cache-Control"), "no-store")
	mustMatch(t, header.Get("Accept-Ranges"), "none")
	mustMatch(t, header.Get("Content-Disposition"),
		`inline; filename="media-studio-2026-09-16T21-02-16Z.mp4"`)
	mustContain(t, header.Values("Link"),
		`<`+testDisplayBase+`/v1/display/displays/boe-1080/screen.mp4?t=1,11&width=960>; rel="https://liken.sh/rel/screen"`)
	mustContain(t, header.Values("Link"),
		`<`+testAudioBase+`/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11>; rel="https://liken.sh/rel/audio"`)
	mustContain(t, header.Values("Link"),
		`<`+testAudioBase+`/v1/audio/sinks/aa-bb-cc-dd-ee-ff/audio.opus?t=1,11>; rel="https://liken.sh/rel/audio"`)
}

// The negotiated composed route alone names its alternates and its
// Content-Location; an extension route sends neither.
func TestTheNegotiatedComposedRouteLinksItsAlternates(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

	negotiated := fixture.call(http.MethodHead, playerPathFor("media"), nil)
	fixed := fixture.call(http.MethodHead, playerPathFor("media.mp4"), nil)

	mustContain(t, negotiated.Header().Values("Link"),
		`</v1/media/namespaces/media/players/studio/media.mkv>; rel="alternate"; type="video/matroska"`)
	mustMatch(t, fixed.Header().Get("Content-Location"), "")
	for _, link := range fixed.Header().Values("Link") {
		if strings.Contains(link, `rel="alternate"`) {
			t.Errorf("an extension route sent %s", link)
		}
	}
}

// Every request writes one log line, with the route template and the
// id the problem document's instance carries.
func TestOneLogLineCarriesTheRequestId(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.allowed = false

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, len(fixture.lines), 1)
	line := fixture.lines[0]
	mustMatch(t, line.Route, apiPlayerTemplate+"/media.mp4")
	mustMatch(t, line.Status, http.StatusForbidden)
	mustMatch(t, strings.HasSuffix(problemOf(t, recorder).Instance, "#"+line.ID), true)
}

func TestHealthAnswersAndReadinessLatches(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.ready.Store(false)

	mustMatch(t, fixture.get(healthTemplate).Code, http.StatusOK)
	mustMatch(t, fixture.get(readyTemplate).Code, http.StatusServiceUnavailable)

	fixture.server.ready.Store(true)

	mustMatch(t, fixture.get(readyTemplate).Code, http.StatusOK)
}

func TestTheRouterReadsEveryTemplateSegment(t *testing.T) {
	rows := []struct {
		template  string
		path      string
		namespace string
		name      string
		ok        bool
	}{
		{apiPlayerTemplate, "/v1/media/namespaces/media/players/studio", "media", "studio", true},
		{apiPlayerTemplate, "/v1/media/namespaces/media/players", "", "", false},
		{apiPlayerTemplate, "/v1/media/namespaces//players/studio", "", "", false},
		{apiBasePath, "/v1/audio", "", "", false},
	}
	for _, row := range rows {
		t.Run(row.path, func(t *testing.T) {
			namespace, name, ok := matchTemplate(row.template, row.path)

			mustMatch(t, ok, row.ok)
			mustMatch(t, namespace, row.namespace)
			mustMatch(t, name, row.name)
		})
	}
}

func problemOf(t *testing.T, recorder *httptest.ResponseRecorder) problemDocument {
	t.Helper()
	var document problemDocument
	body, err := io.ReadAll(recorder.Body)
	mustSucceed(t, err)
	mustSucceed(t, json.Unmarshal(body, &document))
	return document
}

func mustContain(t *testing.T, values []string, wanted string) {
	t.Helper()
	for _, value := range values {
		if value == wanted {
			return
		}
	}
	t.Errorf("got %q, want one of them to be %q", values, wanted)
}
