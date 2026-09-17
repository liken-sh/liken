package main

import (
	"strings"
	"testing"
	"time"
)

func TestASpanParsesInTheOrderSection511States(t *testing.T) {
	cases := []struct {
		query string
		begin time.Duration
		end   time.Duration
		ended bool
	}{
		{"t=0,5", 0, 5 * time.Second, true},
		{"t=5,7", 5 * time.Second, 7 * time.Second, true},
		{"t=5", 5 * time.Second, 0, false},
		{"t=,5", 0, 5 * time.Second, true},
		{"t=npt:10", 10 * time.Second, 0, false},
		{"t=npt:10,20", 10 * time.Second, 20 * time.Second, true},
		{"t=0.5,1.25", 500 * time.Millisecond, 1250 * time.Millisecond, true},
		{"t=00:00:42", 42 * time.Second, 0, false},
		{"t=00:42", 42 * time.Second, 0, false},
		{"t=00:00:01.5,00:00:02", 1500 * time.Millisecond, 2 * time.Second, true},
		// npt-hh is 1*DIGIT, so an hour-long end is a legitimate ask.
		{"t=0,1:00:00", 0, time.Hour, true},
		{"t=0,100:00:00", 0, 100 * time.Hour, true},
		// The three section 6.1.1 cases: the query splits on & and =
		// first, and each part percent-decodes second.
		{"t=10%2C20", 10 * time.Second, 20 * time.Second, true},
		{"t=%6ept:10", 10 * time.Second, 0, false},
		{"t=npt%3a10", 10 * time.Second, 0, false},
		// An absent t= is the whole stream until the client closes.
		{"", 0, 0, false},
		{"bitrate=128", 0, 0, false},
	}
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	for _, row := range cases {
		knobs, err := parseKnobs(route, row.query)
		if err != nil {
			t.Errorf("%q: %v", row.query, err)
			continue
		}
		if knobs.Span.Begin != row.begin {
			t.Errorf("%q: begin is %v, want %v", row.query, knobs.Span.Begin, row.begin)
		}
		if knobs.Span.Ended != row.ended {
			t.Errorf("%q: ended is %v, want %v", row.query, knobs.Span.Ended, row.ended)
		}
		if row.ended && knobs.Span.End != row.end {
			t.Errorf("%q: end is %v, want %v", row.query, knobs.Span.End, row.end)
		}
	}
}

func TestAQueryTheGrammarRefusesIsARefusal(t *testing.T) {
	refused := []string{
		// A repeated dimension. The spec tells a user agent to keep
		// the last one; this API refuses, because a query produces a
		// new resource.
		"t=2&t=10",
		// A begin at or past the end.
		"t=7,5",
		"t=5,5",
		// Past captureBeginMax.
		"t=61",
		"t=61,62",
		// Not NPT.
		"t=smpte:00:00:10",
		"t=clock:2026-09-16T21:02:16Z",
		"t=ten",
		"t=",
		"t=,",
		"t=-1,5",
		"t=1,2,3",
		"t=99:99",
		// ParseFloat takes these; the NPT grammar does not produce
		// any of them, and a NaN begin passes every comparison a
		// number would fail.
		"t=nan",
		"t=nan,inf",
		"t=inf",
		"t=+Inf",
		"t=infinity",
		"t=1e3",
		"t=+5",
		"t=-5",
		"t=0x10",
		"t= 5",
		"t=5 ",
		"t=5.",
		"t=.5",
		"t=1:2:3",
		"t=1:60",
		"t=00:60",
		"t=npt:nan,npt:inf",
		// A knob this API does not take at all.
		"framerate=15",
		"xywh=0,0,10,10",
		"t=0,5&width=480",
		// A bitrate outside the range opusenc takes.
		"bitrate=5",
		"bitrate=257",
		"bitrate=fast",
		"bitrate=128&bitrate=96",
	}
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	for _, query := range refused {
		if _, err := parseKnobs(route, query); err == nil {
			t.Errorf("%q parsed, and the grammar refuses it", query)
		}
	}
}

func TestOnlyOpusTakesABitrate(t *testing.T) {
	opus, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	knobs, err := parseKnobs(opus, "bitrate=128")
	if err != nil {
		t.Fatalf("opus refused a bitrate: %v", err)
	}
	if knobs.Bitrate != 128 {
		t.Errorf("the bitrate is %d, want 128", knobs.Bitrate)
	}
	for _, path := range []string{
		"/v1/audio/sinks/kitchen/audio.wav",
		"/v1/audio/sinks/kitchen/audio.flac",
	} {
		route, _, _ := matchRoute(path)
		if _, err := parseKnobs(route, "bitrate=128"); err == nil {
			t.Errorf("%s took a bitrate", path)
		}
	}
}

func TestADocumentRouteTakesNoKnobs(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio")
	if _, err := parseKnobs(route, "t=0,5"); err == nil {
		t.Error("the discovery document took a span")
	}
	if _, err := parseKnobs(route, ""); err != nil {
		t.Errorf("the discovery document refused an empty query: %v", err)
	}
}

// The round trip the plan asks for: expand the template the discovery
// document publishes, then call the result.
func TestThePublishedTemplateExpandsIntoACallableQuery(t *testing.T) {
	template := aspectTemplate("sinks", "audio")
	if template != "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}" {
		t.Fatalf("the published template is %q", template)
	}
	// RFC 6570 form-style query expansion percent-encodes the comma,
	// which is exactly the t=10%2C20 case above.
	expanded := expandAspectTemplate(template, "kitchen", "opus", map[string]string{
		"t":       "5,7",
		"bitrate": "128",
	})
	want := "/v1/audio/sinks/kitchen/audio.opus?t=5%2C7&bitrate=128"
	if expanded != want {
		t.Fatalf("the expansion is %q, want %q", expanded, want)
	}
	path, query, _ := strings.Cut(expanded, "?")
	route, name, found := matchRoute(path)
	if !found {
		t.Fatalf("the expansion matched no route: %s", path)
	}
	if name != "kitchen" {
		t.Errorf("the expansion named %q", name)
	}
	knobs, err := parseKnobs(route, query)
	if err != nil {
		t.Fatalf("the expansion did not parse: %v", err)
	}
	if knobs.Span.Begin != 5*time.Second || knobs.Span.End != 7*time.Second || knobs.Bitrate != 128 {
		t.Errorf("the expansion parsed as %+v", knobs)
	}
}

func TestASpanCarriesTheBytesItWants(t *testing.T) {
	// s16le at 48000 Hz and two channels is 192,000 bytes a second.
	span := timeSpan{Begin: 5 * time.Second, End: 7 * time.Second, Ended: true}
	if got := span.bodyBytes(48000, 2); got != 384000 {
		t.Errorf("the body is %d bytes, want 384000", got)
	}
	open := timeSpan{}
	if got := open.bodyBytes(48000, 2); got != 0 {
		t.Errorf("an open span bounds the body at %d bytes", got)
	}
}
