package main

import (
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
)

// The site serves this file at /v1/audio/openapi.json, through the
// static mount in docs/hugo.yaml.
const committedOpenAPI = "docs/static/v1/audio/openapi.json"

// updateOpenAPI makes the comparison test write the committed copy
// instead of comparing against it. make openapi passes it.
var updateOpenAPI = flag.Bool("update-openapi", false,
	"write docs/static/v1/audio/openapi.json from the router table")

func TestTheOpenAPIDocumentPublishesEveryRoute(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	published := openAPIPaths(document)
	var table []string
	for _, route := range apiRoutes {
		table = append(table, route.Template)
	}
	slices.Sort(table)
	if !slices.Equal(published, table) {
		t.Errorf("the document publishes %v, want the router table's %v", published, table)
	}
}

func TestEveryPathItemAnswersTheThreeMethods(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	for path, raw := range document.Paths {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s is %T, want a path item", path, raw)
		}
		for _, method := range []string{"get", "head", "options"} {
			if _, found := item[method]; !found {
				t.Errorf("%s publishes no %s, want get, head, and options", path, method)
			}
		}
		for _, method := range []string{"post", "put", "patch", "delete"} {
			if _, found := item[method]; found {
				t.Errorf("%s publishes %s, want only get, head, and options", path, method)
			}
		}
	}
}

func TestEachExtensionPathServesOneContentType(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	cases := map[string][]string{
		"/v1/audio/sinks/{name}/audio.wav":  {"audio/wav"},
		"/v1/audio/sinks/{name}/audio.flac": {"audio/flac"},
		"/v1/audio/sinks/{name}/audio.opus": {"audio/ogg; codecs=opus"},
		"/v1/audio/sinks/{name}/audio": {
			"audio/wav", "audio/flac", "audio/ogg; codecs=opus",
		},
	}
	for path, want := range cases {
		content := contentTypesOf(t, document, path)
		slices.Sort(want)
		slices.Sort(content)
		if !slices.Equal(content, want) {
			t.Errorf("%s serves %v, want %v", path, content, want)
		}
	}
}

func contentTypesOf(t *testing.T, document openAPIDocument, path string) []string {
	t.Helper()
	item, ok := document.Paths[path].(map[string]any)
	if !ok {
		t.Fatalf("the document has no path item %s, want one", path)
	}
	operation := item["get"].(map[string]any)
	responses := operation["responses"].(map[string]any)
	answer := responses["200"].(map[string]any)
	content := answer["content"].(map[string]any)
	var types []string
	for name := range content {
		types = append(types, name)
	}
	return types
}

func TestOnlyOpusPathsPublishTheBitrateParameter(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	cases := map[string]bool{
		"/v1/audio/sinks/{name}/audio.opus": true,
		"/v1/audio/sinks/{name}/audio":      true,
		"/v1/audio/sinks/{name}/audio.wav":  false,
		"/v1/audio/sinks/{name}/audio.flac": false,
		"/v1/audio/sinks/{name}":            false,
	}
	for path, wanted := range cases {
		item := document.Paths[path].(map[string]any)
		operation := item["get"].(map[string]any)
		found := false
		for _, raw := range operation["parameters"].([]any) {
			if raw.(map[string]any)["name"] == "bitrate" {
				found = true
			}
		}
		if found != wanted {
			t.Errorf("%s publishes bitrate = %v, want %v", path, found, wanted)
		}
	}
}

func TestTheServedAndCommittedDocumentsDifferOnlyInServers(t *testing.T) {
	if *updateOpenAPI {
		body, err := renderOpenAPI(newOpenAPI(openAPIPlaceholderServer))
		if err != nil {
			t.Fatalf("rendering the document: %v", err)
		}
		if err := os.WriteFile(committedOpenAPI, body, 0o644); err != nil {
			t.Fatalf("writing the committed document: %v", err)
		}
		t.Logf("wrote %s", committedOpenAPI)
		return
	}
	committed, err := os.ReadFile(committedOpenAPI)
	if err != nil {
		t.Fatalf("reading the committed document: %v", err)
	}
	served, err := renderOpenAPI(newOpenAPI("https://audio-api.liken-system.svc:8443"))
	if err != nil {
		t.Fatalf("rendering the served document: %v", err)
	}
	same, err := sameApartFromServers(committed, served)
	if err != nil {
		t.Fatalf("comparing the two documents: %v", err)
	}
	if !same {
		t.Error("the committed document and the served one differ in more than servers, want only servers to differ. " +
			"Run make openapi and commit what it writes")
	}
}

func TestTheCommittedDocumentHoldsThePlaceholderOrigin(t *testing.T) {
	if *updateOpenAPI {
		t.Skip("the generator writes the placeholder itself")
	}
	committed, err := os.ReadFile(committedOpenAPI)
	if err != nil {
		t.Fatalf("reading the committed document: %v", err)
	}
	var document struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(committed, &document); err != nil {
		t.Fatalf("parsing the committed document: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != openAPIPlaceholderServer {
		t.Errorf("the committed document names the servers %v, want only %s", document.Servers, openAPIPlaceholderServer)
	}
}

func TestTheDocumentNamesItsOwnProblemResponses(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	item := document.Paths["/v1/audio/sinks/{name}/audio.wav"].(map[string]any)
	responses := item["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"400", "401", "403", "404", "405", "406", "409", "500", "502", "503", "504"} {
		if _, found := responses[status]; !found {
			t.Errorf("a tap route publishes no %s, want one", status)
		}
	}
	if _, found := responses["304"]; found {
		t.Error("a tap route publishes a 304, want none, because a tap has no ETag")
	}

	discovery := document.Paths["/v1/audio"].(map[string]any)
	documentResponses := discovery["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"400", "304"} {
		if _, found := documentResponses[status]; !found {
			t.Errorf("the discovery document publishes no %s, want one", status)
		}
	}
	for _, status := range []string{"404", "409", "500", "502", "503", "504"} {
		if _, found := documentResponses[status]; found {
			t.Errorf("the discovery document publishes a %s, want none, because a document route has no capture failure", status)
		}
	}

	info := document.Paths["/v1/audio/sinks/{name}"].(map[string]any)
	infoResponses := info["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"304", "404", "409", "502", "503", "504"} {
		if _, found := infoResponses[status]; !found {
			t.Errorf("the info route publishes no %s, want one", status)
		}
	}
	if _, found := infoResponses["500"]; found {
		t.Error("the info route publishes a 500, want none, because only a tap returns wrongTarget")
	}
}

func TestThePublishedDocumentCarriesNoAuthoringInstruction(t *testing.T) {
	// The site serves the committed copy, so an instruction to the author
	// left in it would be published.
	committed, err := os.ReadFile(committedOpenAPI)
	if err != nil {
		t.Fatalf("reading the committed document: %v", err)
	}
	if strings.Contains(string(committed), "PROSE:") {
		t.Error("the published document contains PROSE:, want no authoring instruction")
	}
	var document map[string]any
	if err := json.Unmarshal(committed, &document); err != nil {
		t.Fatalf("parsing the committed document: %v", err)
	}
	summaries := map[string]int{}
	for _, raw := range document["paths"].(map[string]any) {
		item := raw.(map[string]any)
		summaries[item["get"].(map[string]any)["summary"].(string)]++
	}
	for summary, count := range summaries {
		if count > 1 {
			t.Errorf("%d routes share the summary %q, want one route for each summary", count, summary)
		}
	}
}

func TestTheDiscoveryDocumentIsTheSharedShape(t *testing.T) {
	body, err := json.Marshal(newDiscovery())
	if err != nil {
		t.Fatalf("encoding the discovery document: %v", err)
	}
	var read map[string]any
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatalf("decoding the discovery document: %v", err)
	}
	if read["openapi"] != "/v1/audio/openapi.json" {
		t.Errorf("the discovery document names the OpenAPI document %v, want /v1/audio/openapi.json", read["openapi"])
	}
	resources := read["resources"].(map[string]any)
	for _, plural := range []string{"sinks", "sources"} {
		resource, found := resources[plural].(map[string]any)
		if !found {
			t.Fatalf("the discovery document has no resource %s, want sinks and sources", plural)
		}
		if resource["self"] != "/v1/audio/"+plural+"/{name}" {
			t.Errorf("%s has self %v, want /v1/audio/%s/{name}", plural, resource["self"], plural)
		}
		aspect := resource["aspects"].(map[string]any)["audio"].(map[string]any)
		if aspect["template"] != "/v1/audio/"+plural+"/{name}/audio{.ext}{?t,bitrate}" {
			t.Errorf("%s publishes the template %v, want /v1/audio/%s/{name}/audio{.ext}{?t,bitrate}", plural, aspect["template"], plural)
		}
		if aspect["redirects"] != false {
			t.Errorf("%s has redirects %v, want false, because an audio route responds with the sound", plural, aspect["redirects"])
		}
		types := aspect["mediaTypes"].([]any)
		if len(types) != 3 || types[0] != "audio/wav" {
			t.Errorf("%s publishes the media types %v, want three, with audio/wav first", plural, types)
		}
		// The type attribute of a Link has no media type parameters (RFC 8288
		// section 3.4.1), so the discovery document lists audio/ogg without
		// codecs=opus.
		for _, name := range types {
			if strings.Contains(name.(string), ";") {
				t.Errorf("the discovery document publishes %q, want no media type parameter", name)
			}
		}
	}
}
