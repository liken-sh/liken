package main

// These tests cover the document routes: the discovery shape and the
// lists copied from the siblings, the info document's facts and stream
// count, the conditional read with If-None-Match, and that the
// committed OpenAPI document names every route the router serves.

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"reflect"
	"sort"
	"testing"
)

// updateOpenAPI is the switch that rewrites the committed document
// from the router. `go generate` runs the test with it set.
var updateOpenAPI = flag.Bool("update-openapi", false, "write openapi.json from the router")

func TestDiscoveryNamesEveryAspectOfAPlayer(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.display.status = http.StatusNotFound
	fixture.audio.status = http.StatusNotFound

	recorder := fixture.get(apiBasePath)

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, recorder.Header().Get("Content-Type"), jsonDocumentType)
	mustMatch(t, recorder.Header().Get("Cache-Control"), "no-cache")
	var document discoveryDocument
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	mustMatch(t, document.OpenAPI, apiBasePath+"/openapi.json")
	players := document.Resources["players"]
	mustMatch(t, players.Self, apiPlayerTemplate)
	mustMatch(t, players.Aspects["screen"].Redirects, true)
	mustMatch(t, players.Aspects["audio"].Redirects, true)
	composed := players.Aspects["media"]
	mustMatch(t, composed.Redirects, false)
	mustMatch(t, composed.LeadIn, composeLeadInWord)
	mustMatch(t, composed.Template,
		apiPlayerTemplate+"/media{.ext}{?t,xywh,width,height,framerate,quality,bitrate}")
	mustMatchAll(t, composed.MediaTypes, []string{"video/mp4", "video/matroska"})
	mustMatchAll(t, composed.Extensions, []string{"mp4", "mkv"})
}

// The screen and audio lists are the siblings' own: what the display
// API and the audio API publish replaces this API's copies.
func TestDiscoveryCopiesTheSiblingsLists(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.display.contentType = jsonDocumentType
	fixture.display.body = siblingDiscovery(t, "displays", "screen",
		[]string{"image/png", "image/avif"}, []string{"png", "avif"})
	fixture.audio.contentType = jsonDocumentType
	fixture.audio.body = siblingDiscovery(t, "sinks", "audio",
		[]string{"audio/wav"}, []string{"wav"})

	recorder := fixture.get(apiBasePath)

	var document discoveryDocument
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	players := document.Resources["players"]
	mustMatchAll(t, players.Aspects["screen"].MediaTypes, []string{"image/png", "image/avif"})
	mustMatchAll(t, players.Aspects["screen"].Extensions, []string{"png", "avif"})
	mustMatchAll(t, players.Aspects["audio"].Extensions, []string{"wav"})
}

// A sibling that will not answer leaves this API's own list in place,
// so discovery never shows an empty aspect.
func TestDiscoveryKeepsItsOwnListWhenASiblingIsSilent(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.display = "http://127.0.0.1:1"

	recorder := fixture.get(apiBasePath)

	var document discoveryDocument
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	mustMatchAll(t, document.Resources["players"].Aspects["screen"].Extensions,
		[]string{"png", "jpg", "mp4", "mjpeg"})
}

func siblingDiscovery(t *testing.T, plural, aspect string, types, extensions []string) []byte {
	t.Helper()
	body, err := json.Marshal(discoveryDocument{
		Resources: map[string]discoveryResource{
			plural: {Aspects: map[string]discoveryAspect{
				aspect: {MediaTypes: types, Extensions: extensions},
			}},
		},
	})
	mustSucceed(t, err)
	return body
}

// The info document carries the facts a client reads before it asks
// for a capture: the Display and its node, the Sinks, whether a Play
// runs, the stream count, and a related link to every capture route.
func TestTheInfoDocumentAnswersTheStreamCount(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

	recorder := fixture.get(playerPathFor(""))

	mustMatch(t, recorder.Code, http.StatusOK)
	var document playerInfoDocument
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	mustMatch(t, document.Namespace, testAPINamespace)
	mustMatch(t, document.Name, testAPIPlayer)
	mustMatch(t, document.Display.Name, testAPIMonitor)
	mustMatch(t, document.Display.Node, "stick1")
	mustMatch(t, document.Playing, true)
	mustMatch(t, document.Streams, 3)
	mustMatch(t, len(document.Sinks), 2)
	links := recorder.Header().Values("Link")
	mustContain(t, links, `</v1/media/namespaces/media/players/studio/screen>; rel="related"`)
	mustContain(t, links, `</v1/media/namespaces/media/players/studio/audio>; rel="related"`)
	mustContain(t, links, `</v1/media/namespaces/media/players/studio/media>; rel="related"`)
}

// An idle unit reports the sinks it remembers and a stream count of
// one, the screen, because a sink is tappable only while a Play runs.
func TestTheInfoDocumentShowsAnIdleUnitsRememberedSinks(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, false)

	recorder := fixture.get(playerPathFor(""))

	var document playerInfoDocument
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	mustMatch(t, document.Playing, false)
	mustMatch(t, document.Streams, 1)
	mustMatch(t, len(document.Sinks), 2)
}

// Every document route answers a matching If-None-Match with 304, no
// body, and Vary: Accept.
func TestADocumentRouteAnswersIfNoneMatch(t *testing.T) {
	rows := []string{apiBasePath, apiBasePath + "/openapi.json", playerPathFor("")}
	for _, row := range rows {
		t.Run(row, func(t *testing.T) {
			fixture := newAPIFixture(t)

			first := fixture.get(row)
			tag := first.Header().Get("ETag")
			second := fixture.call(http.MethodGet, row, http.Header{"If-None-Match": {tag}})

			mustMatch(t, second.Code, http.StatusNotModified)
			mustMatch(t, second.Body.Len(), 0)
			mustMatch(t, second.Header().Get("Vary"), "Accept")
		})
	}
}

// The two documents the router compiles in carry the build version as
// their ETag.
func TestTheCompiledDocumentsCarryTheBuildVersion(t *testing.T) {
	fixture := newAPIFixture(t)

	for _, route := range []string{apiBasePath, apiBasePath + "/openapi.json"} {
		mustMatch(t, fixture.get(route).Header().Get("ETag"), `"test"`)
	}
}

// The info document is validated by its own body and not the build
// version, so a Player that changed answers 200 with a new ETag and
// not 304.
func TestTheInfoDocumentIsValidatedByItsOwnBody(t *testing.T) {
	fixture := newAPIFixture(t)

	first := fixture.get(playerPathFor(""))
	tag := first.Header().Get("ETag")
	unchanged := fixture.call(http.MethodGet, playerPathFor(""), http.Header{"If-None-Match": {tag}})
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)
	changed := fixture.call(http.MethodGet, playerPathFor(""), http.Header{"If-None-Match": {tag}})

	mustMatch(t, tag != `"test"`, true)
	mustMatch(t, unchanged.Code, http.StatusNotModified)
	mustMatch(t, changed.Code, http.StatusOK)
	mustMatch(t, changed.Header().Get("ETag") != tag, true)
}

// A document route carries none of the capture fields: no file name,
// no range statement, and no Location.
func TestADocumentRouteCarriesNoCaptureFields(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.get(playerPathFor(""))

	for _, field := range []string{"Content-Disposition", "Accept-Ranges", "Location"} {
		mustMatch(t, recorder.Header().Get(field), "")
	}
}

// The committed OpenAPI document names every route the router serves
// and no others, so a route cannot be added without documenting it.
func TestTheOpenAPIDocumentNamesEveryRoute(t *testing.T) {
	server := &apiServer{}
	served := make([]string, 0)
	for _, route := range server.routes() {
		served = append(served, route.template)
	}
	sort.Strings(served)

	mustMatchAll(t, openAPIPaths(committedOpenAPI), served)
}

// The committed copy is what the router produces, servers aside. With
// the switch set, the test rewrites it instead of comparing.
func TestTheOpenAPIDocumentMatchesTheRouter(t *testing.T) {
	generated := openAPIDocument()
	if *updateOpenAPI {
		mustSucceed(t, os.WriteFile("openapi.json", generated, 0o644))
		return
	}

	if !reflect.DeepEqual(withoutServers(t, generated), withoutServers(t, committedOpenAPI)) {
		t.Error("openapi.json is not what the router produces; run go generate ./...")
	}
}

func withoutServers(t *testing.T, document []byte) map[string]json.RawMessage {
	t.Helper()
	var read map[string]json.RawMessage
	mustSucceed(t, json.Unmarshal(document, &read))
	delete(read, "servers")
	return read
}

// The served copy names the origin the request reached in its servers
// list, in place of the committed placeholder.
func TestTheServedOpenAPINamesTheOrigin(t *testing.T) {
	fixture := newAPIFixture(t)

	recorder := fixture.get(apiBasePath + "/openapi.json")

	mustMatch(t, recorder.Header().Get("Content-Type"), openAPIDocumentType)
	var document struct {
		Servers []openAPIServer `json:"servers"`
	}
	mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
	mustMatch(t, document.Servers[0].URL, "https://example.com")
}

// One round trip expands the published RFC 6570 template and calls
// what it produces, so the template and the router cannot drift apart.
func TestThePublishedTemplateExpandsToARouteThatAnswers(t *testing.T) {
	fixture := newAPIFixture(t)
	var document discoveryDocument
	mustSucceed(t, json.Unmarshal(fixture.get(apiBasePath).Body.Bytes(), &document))

	template := document.Resources["players"].Aspects["media"].Template
	expanded := expandTemplate(template, testAPINamespace, testAPIPlayer, "mp4", "t=0,10")

	recorder := fixture.call(http.MethodHead, expanded, nil)

	mustMatch(t, expanded,
		playerPathFor("media.mp4")+"?t=0,10")
	mustMatch(t, recorder.Code, http.StatusOK)
}

// expandTemplate does the RFC 6570 level 3 expansion this round trip
// needs and no more: the two path variables, the extension, and the
// form query.
func expandTemplate(template, namespace, name, extension, query string) string {
	expanded := template
	for _, pair := range [][2]string{
		{"{namespace}", namespace},
		{"{name}", name},
		{"{.ext}", "." + extension},
	} {
		expanded = replaceOnce(expanded, pair[0], pair[1])
	}
	if index := indexOf(expanded, "{?"); index >= 0 {
		expanded = expanded[:index]
	}
	return expanded + "?" + query
}

func replaceOnce(text, from, to string) string {
	index := indexOf(text, from)
	if index < 0 {
		return text
	}
	return text[:index] + to + text[index+len(from):]
}

func indexOf(text, part string) int {
	for index := 0; index+len(part) <= len(text); index++ {
		if text[index:index+len(part)] == part {
			return index
		}
	}
	return -1
}
