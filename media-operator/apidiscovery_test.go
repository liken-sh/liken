package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"testing"
	"testing/synctest"
)

// updateOpenAPI makes the test rewrite the committed OpenAPI document
// from the router instead of comparing against it. `go generate` runs
// the test with it set.
var updateOpenAPI = flag.Bool("update-openapi", false, "write openapi.json from the router")

func TestDiscoveryNamesEveryAspectOfAPlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// The media types and extensions that the display API and the audio API
// publish replace this API's built-in lists.
func TestDiscoveryCopiesTheSiblingsLists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// When a sibling does not respond, discovery keeps its built-in list, so
// an aspect is never empty.
func TestDiscoveryKeepsItsOwnListWhenASiblingIsSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.display = "http://127.0.0.1:1"

		recorder := fixture.get(apiBasePath)

		var document discoveryDocument
		mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
		mustMatchAll(t, document.Resources["players"].Aspects["screen"].Extensions,
			[]string{"png", "jpg", "mp4", "mjpeg"})
	})
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

// The info document gives the Display and its node, the Sinks, whether a
// Play runs, and the stream count, and links to every capture route.
func TestTheInfoDocumentDescribesThePlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)

		recorder := fixture.get(playerPathFor(""))

		mustMatch(t, recorder.Code, http.StatusOK)
		var document playerInfoDocument
		mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
		mustMatch(t, document.Namespace, testAPINamespace)
		mustMatch(t, document.Name, testAPIPlayer)
		mustMatch(t, document.Display.Name, testAPIMonitor)
		mustMatch(t, document.Display.Node, "screen-1")
		mustMatch(t, document.Playing, true)
		mustMatch(t, document.Streams, 3)
		mustMatch(t, len(document.Sinks), 2)
		links := recorder.Header().Values("Link")
		mustContain(t, links, `</v1/media/namespaces/media/players/studio/screen>; rel="related"`)
		mustContain(t, links, `</v1/media/namespaces/media/players/studio/audio>; rel="related"`)
		mustContain(t, links, `</v1/media/namespaces/media/players/studio/media>; rel="related"`)
	})
}

// An idle Player lists the sinks in its status and has a stream count of
// one, the screen, because a sink can be tapped only while a Play runs.
func TestTheInfoDocumentOfAnIdlePlayerCountsOnlyTheScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, false)

		recorder := fixture.get(playerPathFor(""))

		var document playerInfoDocument
		mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
		mustMatch(t, document.Playing, false)
		mustMatch(t, document.Streams, 1)
		mustMatch(t, len(document.Sinks), 2)
	})
}

// Every document route responds to a matching If-None-Match with 304, no
// body, and Vary: Accept.
func TestADocumentRouteRespondsToIfNoneMatch(t *testing.T) {
	rows := []string{apiBasePath, apiBasePath + "/openapi.json", playerPathFor("")}
	for _, row := range rows {
		t.Run(row, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAPIFixture(t)

				first := fixture.get(row)
				tag := first.Header().Get("ETag")
				second := fixture.call(http.MethodGet, row, http.Header{"If-None-Match": {tag}})

				mustMatch(t, second.Code, http.StatusNotModified)
				mustMatch(t, second.Body.Len(), 0)
				mustMatch(t, second.Header().Get("Vary"), "Accept")
			})
		})
	}
}

// The discovery document copies the siblings' lists, so a sibling that
// adds a form changes the body without a release. A client that sends
// the first ETag then gets the new body and not 304.
func TestTheDiscoveryDocumentIsValidatedByItsOwnBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.display.contentType = jsonDocumentType
		fixture.display.body = siblingDiscovery(t, "displays", "screen", []string{"image/png"}, []string{"png"})

		tag := fixture.get(apiBasePath).Header().Get("ETag")
		fixture.display.body = siblingDiscovery(t, "displays", "screen",
			[]string{"image/png", "image/avif"}, []string{"png", "avif"})
		changed := fixture.call(http.MethodGet, apiBasePath, http.Header{"If-None-Match": {tag}})

		if changed.Code != http.StatusOK {
			t.Errorf("a read with the first ETag %s after the display API added avif answered %d, want 200 with the new list", tag, changed.Code)
		}
	})
}

// With no public base URL, the OpenAPI document names the origin of the
// request, so a read from another origin gets its own body and not 304.
func TestTheOpenAPIDocumentIsValidatedByItsOwnBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		route := apiBasePath + "/openapi.json"

		tag := fixture.get(route).Header().Get("ETag")
		request := httptest.NewRequest(http.MethodGet, route, nil)
		request.Host = "media-api.example.org"
		request.Header.Set("Authorization", "Bearer "+testAPIToken)
		request.Header.Set("If-None-Match", tag)
		other := httptest.NewRecorder()
		fixture.server.handler().ServeHTTP(other, request)

		if other.Code != http.StatusOK {
			t.Errorf("a read from another origin with the ETag %s answered %d, want 200 with that origin in servers", tag, other.Code)
		}
	})
}

// The info document's ETag comes from its body and not the build version,
// so a changed Player gets 200 with a new ETag and not 304.
func TestTheInfoDocumentIsValidatedByItsOwnBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestADocumentRouteSendsNoCaptureHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		recorder := fixture.get(playerPathFor(""))

		for _, field := range []string{"Content-Disposition", "Accept-Ranges", "Location"} {
			mustMatch(t, recorder.Header().Get(field), "")
		}
	})
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

// The committed document equals what the router produces, except for the
// servers list.
func TestTheOpenAPIDocumentMatchesTheRouter(t *testing.T) {
	generated := openAPIDocument()
	if *updateOpenAPI {
		mustSucceed(t, os.WriteFile("openapi.json", generated, 0o644))
		return
	}

	if !reflect.DeepEqual(withoutServers(t, generated), withoutServers(t, committedOpenAPI)) {
		t.Error("openapi.json differs from the document that the router produces. Run go generate ./... to write it again")
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
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)

		recorder := fixture.get(apiBasePath + "/openapi.json")

		mustMatch(t, recorder.Header().Get("Content-Type"), openAPIDocumentType)
		var document struct {
			Servers []openAPIServer `json:"servers"`
		}
		mustSucceed(t, json.Unmarshal(recorder.Body.Bytes(), &document))
		mustMatch(t, document.Servers[0].URL, "https://example.com")
	})
}

// The test expands the media template from the discovery document and
// requests the result, so the template and the router cannot differ.
func TestThePublishedTemplateExpandsToAServedRoute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		var document discoveryDocument
		mustSucceed(t, json.Unmarshal(fixture.get(apiBasePath).Body.Bytes(), &document))

		template := document.Resources["players"].Aspects["media"].Template
		expanded := expandTemplate(template, testAPINamespace, testAPIPlayer, "mp4", "t=0,10")

		recorder := fixture.call(http.MethodHead, expanded, nil)

		mustMatch(t, expanded,
			playerPathFor("media.mp4")+"?t=0,10")
		mustMatch(t, recorder.Code, http.StatusOK)
	})
}

// expandTemplate does the part of RFC 6570 expansion that the templates
// use: the two path variables, the extension, and the form-style query.
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
