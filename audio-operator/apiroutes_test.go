package main

import (
	"slices"
	"testing"
)

func TestTheRouteTableHoldsEveryPublishedRoute(t *testing.T) {
	published := []string{
		"/v1/audio",
		"/v1/audio/openapi.json",
		"/v1/audio/sinks/{name}",
		"/v1/audio/sinks/{name}/audio",
		"/v1/audio/sinks/{name}/audio.wav",
		"/v1/audio/sinks/{name}/audio.flac",
		"/v1/audio/sinks/{name}/audio.opus",
		"/v1/audio/sources/{name}",
		"/v1/audio/sources/{name}/audio",
		"/v1/audio/sources/{name}/audio.wav",
		"/v1/audio/sources/{name}/audio.flac",
		"/v1/audio/sources/{name}/audio.opus",
	}
	var held []string
	for _, route := range apiRoutes {
		held = append(held, route.Template)
	}
	slices.Sort(published)
	slices.Sort(held)
	if !slices.Equal(published, held) {
		t.Errorf("the table holds %v, the manual publishes %v", held, published)
	}
}

func TestEveryRouteMatchesItsOwnPath(t *testing.T) {
	cases := []struct {
		path     string
		template string
		name     string
	}{
		{"/v1/audio", "/v1/audio", ""},
		{"/v1/audio/openapi.json", "/v1/audio/openapi.json", ""},
		{"/v1/audio/sinks/kitchen", "/v1/audio/sinks/{name}", "kitchen"},
		{"/v1/audio/sinks/kitchen/audio", "/v1/audio/sinks/{name}/audio", "kitchen"},
		{"/v1/audio/sinks/kitchen/audio.wav", "/v1/audio/sinks/{name}/audio.wav", "kitchen"},
		{"/v1/audio/sinks/kitchen/audio.flac", "/v1/audio/sinks/{name}/audio.flac", "kitchen"},
		{"/v1/audio/sinks/kitchen/audio.opus", "/v1/audio/sinks/{name}/audio.opus", "kitchen"},
		{"/v1/audio/sources/desk-mic/audio.wav", "/v1/audio/sources/{name}/audio.wav", "desk-mic"},
		{"/v1/audio/sources/desk-mic", "/v1/audio/sources/{name}", "desk-mic"},
		{
			"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.opus",
			"/v1/audio/sinks/{name}/audio.opus",
			"usb-0573-1573-a34004801402-usb-audio",
		},
	}
	for _, row := range cases {
		route, name, found := matchRoute(row.path)
		if !found {
			t.Errorf("%s matched no route", row.path)
			continue
		}
		if route.Template != row.template {
			t.Errorf("%s matched %s, want %s", row.path, route.Template, row.template)
		}
		if name != row.name {
			t.Errorf("%s carried the name %q, want %q", row.path, name, row.name)
		}
	}
}

func TestPathsOutsideTheTableMatchNothing(t *testing.T) {
	outside := []string{
		"/",
		"/v1",
		"/v1/video/sinks/kitchen/audio.wav",
		"/v1/audio/sinks",
		"/v1/audio/sinks/kitchen/levels",
		"/v1/audio/sinks/kitchen/audio.mp3",
		"/v1/audio/sinks/kitchen/audio.wav/extra",
		"/v1/audio/namespaces/liken-system/sinks/kitchen/audio.wav",
	}
	for _, path := range outside {
		if _, _, found := matchRoute(path); found {
			t.Errorf("%s matched a route", path)
		}
	}
}

func TestTapRoutesServeTheRightRepresentations(t *testing.T) {
	cases := []struct {
		path  string
		serve []string
	}{
		{"/v1/audio/sinks/kitchen/audio", []string{"audio/wav", "audio/flac", "audio/ogg"}},
		{"/v1/audio/sinks/kitchen/audio.wav", []string{"audio/wav"}},
		{"/v1/audio/sinks/kitchen/audio.flac", []string{"audio/flac"}},
		{"/v1/audio/sinks/kitchen/audio.opus", []string{"audio/ogg"}},
	}
	for _, row := range cases {
		route, _, found := matchRoute(row.path)
		if !found {
			t.Fatalf("%s matched no route", row.path)
		}
		var served []string
		for _, form := range route.Serves {
			served = append(served, form.MediaType)
		}
		if !slices.Equal(served, row.serve) {
			t.Errorf("%s serves %v, want %v", row.path, served, row.serve)
		}
	}
}

func TestOnlyOpusTakesTheBitrateKnob(t *testing.T) {
	cases := map[string]bool{
		"/v1/audio/sinks/kitchen/audio":      true,
		"/v1/audio/sinks/kitchen/audio.opus": true,
		"/v1/audio/sinks/kitchen/audio.wav":  false,
		"/v1/audio/sinks/kitchen/audio.flac": false,
		"/v1/audio/sources/desk/audio.opus":  true,
		"/v1/audio/sinks/kitchen":            false,
		"/v1/audio":                          false,
	}
	for path, takes := range cases {
		route, _, found := matchRoute(path)
		if !found {
			t.Fatalf("%s matched no route", path)
		}
		if route.takes("bitrate") != takes {
			t.Errorf("%s takes bitrate = %v, want %v", path, route.takes("bitrate"), takes)
		}
	}
}

func TestOnlyTapRoutesTakeASpan(t *testing.T) {
	cases := map[string]bool{
		"/v1/audio":                         false,
		"/v1/audio/openapi.json":            false,
		"/v1/audio/sinks/kitchen":           false,
		"/v1/audio/sinks/kitchen/audio":     true,
		"/v1/audio/sinks/kitchen/audio.wav": true,
		"/v1/audio/sources/desk/audio.flac": true,
	}
	for path, takes := range cases {
		route, _, _ := matchRoute(path)
		if route.takes("t") != takes {
			t.Errorf("%s takes t = %v, want %v", path, route.takes("t"), takes)
		}
	}
}

func TestTheSiblingsOfAnExtensionRouteAreTheOtherFormats(t *testing.T) {
	route, name, _ := matchRoute("/v1/audio/sinks/kitchen/audio.flac")
	want := []acceptableType{
		{Type: "audio/wav", Href: "/v1/audio/sinks/kitchen/audio.wav"},
		{Type: "audio/flac", Href: "/v1/audio/sinks/kitchen/audio.flac"},
		{Type: "audio/ogg", Href: "/v1/audio/sinks/kitchen/audio.opus"},
	}
	if got := route.acceptable(name); !slices.Equal(got, want) {
		t.Errorf("acceptable is %v, want %v", got, want)
	}
}

func TestTheAspectTemplateCarriesBothKnobs(t *testing.T) {
	want := "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}"
	if got := aspectTemplate("sinks", "audio"); got != want {
		t.Errorf("the template is %q, want %q", got, want)
	}
}

func TestTheKubernetesObjectIsNamedAbsolutely(t *testing.T) {
	want := "https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen"
	if got := objectURI("sinks", "kitchen"); got != want {
		t.Errorf("the object URI is %q, want %q", got, want)
	}
}
