package main

import (
	"slices"
	"testing"
)

func TestTheNegotiatedRouteAnswersTheManualsTable(t *testing.T) {
	cases := []struct {
		accept    string
		extension string
	}{
		{"", "wav"},
		{"*/*", "wav"},
		{"audio/*", "wav"},
		{"audio/ogg; codecs=opus, audio/flac;q=0.5", "opus"},
		{"audio/flac;q=0.5, audio/ogg;q=0.5", "flac"},
		{"audio/wav;q=0, */*", "flac"},
		{"audio/vnd.wave", "wav"},
		{"audio/wave", "wav"},
		{"audio/x-wav", "wav"},
		{"audio/x-flac", "flac"},
		{"audio/ogg", "opus"},
		{"audio/mpeg, audio/flac", "flac"},
		{"audio/wav;q=0.1, audio/flac;q=0.2, audio/ogg;q=0.3", "opus"},
		{"audio/*;q=0.2, audio/flac;q=0.9", "flac"},
	}
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	for _, row := range cases {
		form, ok := negotiate(route, row.accept)
		if !ok {
			t.Errorf("Accept: %q was refused, want %s", row.accept, row.extension)
			continue
		}
		if form.Extension != row.extension {
			t.Errorf("Accept: %q served %s, want %s", row.accept, form.Extension, row.extension)
		}
	}
}

func TestAnAcceptThatExcludesEveryFormIsRefused(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	for _, accept := range []string{"audio/mpeg", "image/png", "*/*;q=0", "audio/*;q=0", "audio/wav;q=0, audio/flac;q=0, audio/ogg;q=0"} {
		if _, ok := negotiate(route, accept); ok {
			t.Errorf("Accept: %q was served", accept)
		}
	}
}

func TestAnExtensionRouteServesItsOwnFormOrNothing(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	for _, accept := range []string{"", "*/*", "audio/*", "audio/wav", "audio/vnd.wave", "audio/x-wav", "audio/wave"} {
		form, ok := negotiate(route, accept)
		if !ok || form.Extension != "wav" {
			t.Errorf("audio.wav with Accept: %q served %v (%v)", accept, form.Extension, ok)
		}
	}
	for _, accept := range []string{"audio/flac", "audio/ogg", "audio/mpeg", "audio/wav;q=0"} {
		if _, ok := negotiate(route, accept); ok {
			t.Errorf("audio.wav served Accept: %q", accept)
		}
	}
}

func TestARefusedExtensionRouteNamesEverySibling(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.wav")
	want := []acceptableType{
		{Type: "audio/wav", Href: "/v1/audio/sinks/kitchen/audio.wav"},
		{Type: "audio/flac", Href: "/v1/audio/sinks/kitchen/audio.flac"},
		{Type: "audio/ogg", Href: "/v1/audio/sinks/kitchen/audio.opus"},
	}
	if got := route.acceptable(name); !slices.Equal(got, want) {
		t.Errorf("acceptable is %v, want %v", got, want)
	}
}

func TestADocumentRouteNegotiatesItsOwnType(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio")
	for _, accept := range []string{"", "*/*", "application/json", "application/*"} {
		if _, ok := negotiate(route, accept); !ok {
			t.Errorf("the discovery document refused Accept: %q", accept)
		}
	}
	if _, ok := negotiate(route, "text/html"); ok {
		t.Error("the discovery document served text/html")
	}

	openapi, _, _ := matchRoute("/v1/audio/openapi.json")
	for _, accept := range []string{"application/openapi+json", "application/json", "*/*"} {
		if _, ok := negotiate(openapi, accept); !ok {
			t.Errorf("the OpenAPI document refused Accept: %q", accept)
		}
	}
	form, _ := negotiate(openapi, "*/*")
	if form.ContentType != "application/openapi+json" {
		t.Errorf("the OpenAPI document sends %q", form.ContentType)
	}
}

func TestTheContentTypeCarriesTheCodecsParameterAndTheLinkTypeDoesNot(t *testing.T) {
	opus, found := representationFor("opus")
	if !found {
		t.Fatal("no opus representation")
	}
	if opus.ContentType != "audio/ogg; codecs=opus" {
		t.Errorf("Content-Type is %q", opus.ContentType)
	}
	if opus.MediaType != "audio/ogg" {
		t.Errorf("the Link type is %q, and RFC 8288 section 3.4.1 takes no parameters", opus.MediaType)
	}
}

func TestAMalformedAcceptIsReadAsFarAsItGoes(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio")
	// A range with no subtype and a q that is not a number are both
	// things a client sends; neither one refuses the whole header.
	form, ok := negotiate(route, "audio, audio/flac;q=bogus")
	if !ok || form.Extension != "flac" {
		t.Errorf("a malformed Accept served %v (%v)", form.Extension, ok)
	}
}
