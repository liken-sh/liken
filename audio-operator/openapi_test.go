package main

import (
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
)

// committedOpenAPI is the copy the site serves at
// /v1/audio/openapi.json, through the static mount in docs/hugo.yaml.
const committedOpenAPI = "docs/static/v1/audio/openapi.json"

// updateOpenAPI writes the committed copy from the router table
// instead of comparing against it. `make openapi` passes it, which is
// the one command that changes the published document.
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
		t.Errorf("the document publishes %v, the router table holds %v", published, table)
	}
}

func TestEveryPathItemAnswersTheThreeMethods(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	for path, raw := range document.Paths {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s is not a path item", path)
		}
		for _, method := range []string{"get", "head", "options"} {
			if _, found := item[method]; !found {
				t.Errorf("%s answers no %s", path, method)
			}
		}
		for _, method := range []string{"post", "put", "patch", "delete"} {
			if _, found := item[method]; found {
				t.Errorf("%s answers %s, and the API answers three methods", path, method)
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
		t.Fatalf("%s is not published", path)
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

func TestOnlyOpusPathsPublishTheBitrateKnob(t *testing.T) {
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
		t.Error("the committed document and the served one differ in more than servers; " +
			"run the generator and commit what it writes")
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
		t.Fatalf("reading the committed document: %v", err)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != openAPIPlaceholderServer {
		t.Errorf("the committed document names %v", document.Servers)
	}
}

func TestTheDocumentNamesItsOwnProblemResponses(t *testing.T) {
	document := newOpenAPI(openAPIPlaceholderServer)
	item := document.Paths["/v1/audio/sinks/{name}/audio.wav"].(map[string]any)
	responses := item["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"400", "401", "403", "404", "405", "406", "409", "500", "502", "503", "504"} {
		if _, found := responses[status]; !found {
			t.Errorf("a tap route publishes no %s", status)
		}
	}
	// A tap has no ETag, so it never answers If-None-Match.
	if _, found := responses["304"]; found {
		t.Error("a tap route publishes a 304")
	}

	// A document route can answer none of the capture failures, but it
	// does answer 400 on an unknown query parameter and 304 on a
	// matching If-None-Match.
	discovery := document.Paths["/v1/audio"].(map[string]any)
	documentResponses := discovery["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"400", "304"} {
		if _, found := documentResponses[status]; !found {
			t.Errorf("the discovery document publishes no %s", status)
		}
	}
	for _, status := range []string{"404", "409", "500", "502", "503", "504"} {
		if _, found := documentResponses[status]; found {
			t.Errorf("the discovery document publishes a %s", status)
		}
	}

	// An info route reads the object and the node's graph, so it
	// answers what those two reads can fail with.
	info := document.Paths["/v1/audio/sinks/{name}"].(map[string]any)
	infoResponses := info["get"].(map[string]any)["responses"].(map[string]any)
	for _, status := range []string{"304", "404", "409", "502", "503", "504"} {
		if _, found := infoResponses[status]; !found {
			t.Errorf("the info route publishes no %s", status)
		}
	}
	if _, found := infoResponses["500"]; found {
		t.Error("the info route publishes a 500, which is the tap's wrong-target alone")
	}
}

func TestThePublishedDocumentCarriesNoAuthoringInstruction(t *testing.T) {
	// The committed copy is live on the site, so an unauthored string
	// in it is published writing.
	committed, err := os.ReadFile(committedOpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(committed), "PROSE:") {
		t.Error("the published document carries authoring instructions")
	}
	var document map[string]any
	if err := json.Unmarshal(committed, &document); err != nil {
		t.Fatal(err)
	}
	// Every summary and description names the route it belongs to,
	// rather than repeating one string across all twelve.
	summaries := map[string]int{}
	for _, raw := range document["paths"].(map[string]any) {
		item := raw.(map[string]any)
		summaries[item["get"].(map[string]any)["summary"].(string)]++
	}
	for summary, count := range summaries {
		if count > 1 {
			t.Errorf("%d routes share the summary %q", count, summary)
		}
	}
}

func TestTheDiscoveryDocumentIsTheSharedShape(t *testing.T) {
	body, err := json.Marshal(newDiscovery())
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]any
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatal(err)
	}
	if read["openapi"] != "/v1/audio/openapi.json" {
		t.Errorf("the discovery document names %v", read["openapi"])
	}
	resources := read["resources"].(map[string]any)
	for _, plural := range []string{"sinks", "sources"} {
		resource, found := resources[plural].(map[string]any)
		if !found {
			t.Fatalf("the discovery document holds no %s", plural)
		}
		if resource["self"] != "/v1/audio/"+plural+"/{name}" {
			t.Errorf("%s names itself %v", plural, resource["self"])
		}
		aspect := resource["aspects"].(map[string]any)["audio"].(map[string]any)
		if aspect["template"] != "/v1/audio/"+plural+"/{name}/audio{.ext}{?t,bitrate}" {
			t.Errorf("%s publishes the template %v", plural, aspect["template"])
		}
		if aspect["redirects"] != false {
			t.Errorf("%s redirects, and an audio route answers with the sound", plural)
		}
		types := aspect["mediaTypes"].([]any)
		if len(types) != 3 || types[0] != "audio/wav" {
			t.Errorf("%s publishes %v", plural, types)
		}
		// The Link type rule holds here too: no media type parameters.
		for _, name := range types {
			if strings.Contains(name.(string), ";") {
				t.Errorf("the discovery document publishes %q with a parameter", name)
			}
		}
	}
}
