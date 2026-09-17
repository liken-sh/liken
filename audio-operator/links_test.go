package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTheServiceLinksAreOnEveryRouteTheTableHolds(t *testing.T) {
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
		if !strings.Contains(header, `<https://audio.liken.sh/docs/reference/api/>; rel="service-doc"`) {
			t.Errorf("%s carries no service-doc: %s", path, header)
		}
	}
}

func TestARouteThatNamesAnObjectDescribesItAbsolutely(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.flac")
	header := linkHeader(route, name, representation{})
	want := `<https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby"`
	if !strings.Contains(header, want) {
		t.Errorf("the header is %s, and it must carry %s", header, want)
	}

	// The two documents name no object, so neither describes one.
	document, _, _ := matchRoute("/v1/audio")
	if strings.Contains(linkHeader(document, "", representation{}), "describedby") {
		t.Error("the discovery document describes an object it does not name")
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
			t.Errorf("the header is %s, and it must carry %s", header, want)
		}
	}
	// RFC 8288 section 3.4.1: the type parameter takes no media type
	// parameters, so the Opus alternate never says codecs=opus.
	if strings.Contains(header, "codecs") {
		t.Errorf("a Link type carries a media type parameter: %s", header)
	}

	fixed, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	if strings.Contains(linkHeader(fixed, name, representation{}), "alternate") {
		t.Error("an extension route named an alternate")
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
			t.Errorf("the header is %s, and it must carry %s", header, want)
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
	want := `inline; filename="usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav"`
	got := contentDisposition("usb-0573-1573-a34004801402-usb-audio", "wav", at)
	if got != want {
		t.Errorf("the disposition is %q, want %q", got, want)
	}
	if strings.Contains(got, ":") {
		t.Errorf("the save name holds a colon: %q", got)
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
		t.Errorf("a document says Vary: %q", got)
	}
	if got := recorder.Header().Get("ETag"); got != `"v1"` {
		t.Errorf("a document's ETag is %q", got)
	}
	for _, absent := range []string{"Content-Disposition", "Accept-Ranges", "Content-Location"} {
		if got := recorder.Header().Get(absent); got != "" {
			t.Errorf("a document carries %s: %q", absent, got)
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
		t.Errorf("a tap says Vary: %q", got)
	}
	// Content-Location names the fixed form the negotiation chose.
	if got := recorder.Header().Get("Content-Location"); got != "/v1/audio/sinks/kitchen/audio.flac" {
		t.Errorf("a negotiated tap says Content-Location: %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.HasSuffix(got, `.flac"`) {
		t.Errorf("the save name is %q", got)
	}
	if got := recorder.Header().Get("ETag"); got != "" {
		t.Errorf("a tap carries an ETag: %q", got)
	}
}

func TestAnExtensionTapNamesNoContentLocation(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	form, _ := representationFor("opus")
	recorder := httptest.NewRecorder()
	writeTapHeaders(recorder, route, name, form, time.Now())

	if got := recorder.Header().Get("Content-Location"); got != "" {
		t.Errorf("an extension route says Content-Location: %q", got)
	}
	// RFC 9110 section 12.5.5's second purpose: Vary tells the
	// recipient the answer was subject to negotiation, and an Accept
	// can still turn this 200 into a 406.
	if got := recorder.Header().Get("Vary"); got != "Accept" {
		t.Errorf("an extension route says Vary: %q", got)
	}
}
