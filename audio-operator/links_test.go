package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEveryRouteLinksTheServiceDescriptionAndDocument(t *testing.T) {
	for _, path := range []string{
		"/v1/audio",
		"/v1/audio/openapi.json",
		"/v1/audio/sinks/kitchen",
		"/v1/audio/sinks/kitchen/audio",
		"/v1/audio/sinks/kitchen/audio.wav",
	} {
		route, name, _ := matchRoute(path)
		header := linkHeader(route, name, representation{})
		if !strings.Contains(header, `</v1/audio/openapi.json>; rel="service-desc"`) {
			t.Errorf("%s carries no service-desc: %s", path, header)
		}
		if !strings.Contains(header, `<https://liken.sh/audio/docs/reference/api/>; rel="service-doc"`) {
			t.Errorf("%s carries no service-doc: %s", path, header)
		}
	}
}

func TestARouteThatNamesAnObjectLinksItsAbsoluteURI(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.flac")
	header := linkHeader(route, name, representation{})
	want := `<https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby"`
	if !strings.Contains(header, want) {
		t.Errorf("the header is %s, want it to contain %s", header, want)
	}

	document, _, _ := matchRoute("/v1/audio")
	if strings.Contains(linkHeader(document, "", representation{}), "describedby") {
		t.Error("the discovery document has a describedby link, want none because it names no object")
	}
}

func TestOnlyTheNegotiatedRouteNamesItsAlternates(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	header := linkHeader(route, name, representation{})
	for _, want := range []string{
		`</v1/audio/sinks/kitchen/audio.wav>; rel="alternate"; type="audio/wav"`,
		`</v1/audio/sinks/kitchen/audio.flac>; rel="alternate"; type="audio/flac"`,
		`</v1/audio/sinks/kitchen/audio.opus>; rel="alternate"; type="audio/ogg"`,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the header is %s, want it to contain %s", header, want)
		}
	}
	// RFC 8288 section 3.4.1 defines the value of type as
	// type-name "/" subtype-name, with no media type parameters, so the
	// Opus alternate has no codecs=opus.
	if strings.Contains(header, "codecs") {
		t.Errorf("a Link type carries a media type parameter: %s, want none", header)
	}

	fixed, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	if strings.Contains(linkHeader(fixed, name, representation{}), "alternate") {
		t.Error("an extension route named an alternate, want none")
	}
}

func TestAnInfoDocumentRelatesToItsCaptureRoutes(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen")
	header := linkHeader(route, name, representation{})
	for _, want := range []string{
		`</v1/audio/sinks/kitchen/audio.wav>; rel="related"; type="audio/wav"`,
		`</v1/audio/sinks/kitchen/audio.flac>; rel="related"; type="audio/flac"`,
		`</v1/audio/sinks/kitchen/audio.opus>; rel="related"; type="audio/ogg"`,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the header is %s, want it to contain %s", header, want)
		}
	}
}

func TestTheLinkHeaderIsOneFieldLine(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	header := linkHeader(route, name, representation{})
	if strings.ContainsAny(header, "\r\n") {
		t.Errorf("the Link header is more than one field line: %q", header)
	}
	if count := strings.Count(header, "rel="); count != 6 {
		t.Errorf("the header carries %d relations, want 6: %s", count, header)
	}
}

func TestOnlyRegisteredRelationsAreUsed(t *testing.T) {
	registered := map[string]bool{
		"service-desc": true, "service-doc": true,
		"describedby": true, "alternate": true, "related": true,
	}
	for _, path := range []string{
		"/v1/audio", "/v1/audio/openapi.json", "/v1/audio/sinks/kitchen",
		"/v1/audio/sinks/kitchen/audio", "/v1/audio/sinks/kitchen/audio.opus",
		"/v1/audio/sources/desk", "/v1/audio/sources/desk/audio",
	} {
		route, name, _ := matchRoute(path)
		for _, part := range strings.Split(linkHeader(route, name, representation{}), "rel=\"") {
			relation, _, found := strings.Cut(part, "\"")
			if !found || strings.HasPrefix(part, "<") {
				continue
			}
			if !registered[relation] {
				t.Errorf("%s uses the relation %q, which is not registered", path, relation)
			}
		}
	}
}

func TestTheSaveNameReplacesEveryColon(t *testing.T) {
	at := time.Date(2026, 9, 16, 21, 2, 16, 0, time.UTC)
	want := `inline; filename="liken-1-usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav"`
	got := contentDisposition("liken-1-usb-0573-1573-a34004801402-usb-audio", "wav", at)
	if got != want {
		t.Errorf("the disposition is %q, want %q", got, want)
	}
	if strings.Contains(got, ":") {
		t.Errorf("the save name contains a colon: %q, want none", got)
	}
}

func TestADocumentCarriesNoCaptureOnlyHeader(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio")
	recorder := httptest.NewRecorder()
	writeDocumentHeaders(recorder, route, name, representation{MediaType: documentType, ContentType: documentType}, "v1")

	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("a document says Cache-Control: %q, want no-cache", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Accept" {
		t.Errorf("a document says Vary: %q, want Accept", got)
	}
	if got := recorder.Header().Get("ETag"); got != `"v1"` {
		t.Errorf("a document's ETag is %q, want %q", got, `"v1"`)
	}
	for _, absent := range []string{"Content-Disposition", "Accept-Ranges", "Content-Location"} {
		if got := recorder.Header().Get(absent); got != "" {
			t.Errorf("a document carries %s: %q, want no such header", absent, got)
		}
	}
}

func TestATapCarriesTheCaptureHeaders(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	form, _ := representationFor("flac")
	recorder := httptest.NewRecorder()
	at := time.Date(2026, 9, 16, 21, 2, 16, 0, time.UTC)
	writeTapHeaders(recorder, route, name, form, at)

	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("a tap says Cache-Control: %q, want no-store", got)
	}
	if got := recorder.Header().Get("Accept-Ranges"); got != "none" {
		t.Errorf("a tap says Accept-Ranges: %q, want none", got)
	}
	if got := recorder.Header().Get("Vary"); got != "Accept" {
		t.Errorf("a tap says Vary: %q, want Accept", got)
	}
	if got := recorder.Header().Get("Content-Location"); got != "/v1/audio/sinks/kitchen/audio.flac" {
		t.Errorf("a negotiated tap says Content-Location: %q, want /v1/audio/sinks/kitchen/audio.flac", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.HasSuffix(got, `.flac"`) {
		t.Errorf("the save name is %q, want a name that ends in .flac", got)
	}
	if got := recorder.Header().Get("ETag"); got != "" {
		t.Errorf("a tap carries an ETag: %q, want none", got)
	}
}

func TestAnExtensionTapNamesNoContentLocation(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	form, _ := representationFor("opus")
	recorder := httptest.NewRecorder()
	writeTapHeaders(recorder, route, name, form, time.Now())

	if got := recorder.Header().Get("Content-Location"); got != "" {
		t.Errorf("an extension route says Content-Location: %q, want none", got)
	}
	// An extension route has Vary: Accept too, because Accept can still
	// turn its 200 into a 406 (RFC 9110 section 12.5.5).
	if got := recorder.Header().Get("Vary"); got != "Accept" {
		t.Errorf("an extension route says Vary: %q, want Accept", got)
	}
}
