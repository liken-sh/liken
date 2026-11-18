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
		// The hours field is 1*DIGIT, so an end of any number of hours is
		// valid.
		{"t=0,1:00:00", 0, time.Hour, true},
		{"t=0,100:00:00", 0, 100 * time.Hour, true},
		// The percent-encoded cases of section 6.1.1. The query splits on
		// & and = before each part is decoded.
		{"t=10%2C20", 10 * time.Second, 20 * time.Second, true},
		{"t=%6ept:10", 10 * time.Second, 0, false},
		{"t=npt%3a10", 10 * time.Second, 0, false},
		// With no t=, the body is the whole stream until the client
		// closes the connection.
		{"", 0, 0, false},
		{"bitrate=128", 0, 0, false},
	}
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	for _, row := range cases {
		parsed, err := parseCaptureQuery(route, row.query)
		if err != nil {
			t.Errorf("parseCaptureQuery(%q) refused the query: %v, want a span", row.query, err)
			continue
		}
		if parsed.Span.Begin != row.begin {
			t.Errorf("%q: begin is %v, want %v", row.query, parsed.Span.Begin, row.begin)
		}
		if parsed.Span.Ended != row.ended {
			t.Errorf("%q: ended is %v, want %v", row.query, parsed.Span.Ended, row.ended)
		}
		if row.ended && parsed.Span.End != row.end {
			t.Errorf("%q: end is %v, want %v", row.query, parsed.Span.End, row.end)
		}
	}
}

func TestAQueryTheGrammarRefusesIsARefusal(t *testing.T) {
	refused := []string{
		// A repeated dimension. The specification tells a user agent
		// to keep the last one, but this API refuses it, because a
		// query produces a new resource.
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
		// ParseFloat accepts these, and the NPT grammar does not.
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
		// Parameters that no route of this API accepts.
		"framerate=15",
		"xywh=0,0,10,10",
		"t=0,5&width=480",
		// A bitrate out of range, not a number, or repeated.
		"bitrate=5",
		"bitrate=257",
		"bitrate=fast",
		"bitrate=128&bitrate=96",
	}
	route, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	for _, query := range refused {
		if _, err := parseCaptureQuery(route, query); err == nil {
			t.Errorf("parseCaptureQuery(%q) returned no error, want a refusal", query)
		}
	}
}

func TestOnlyOpusTakesABitrate(t *testing.T) {
	opus, _, _ := matchRoute("/v1/audio/sinks/kitchen/audio.opus")
	parsed, err := parseCaptureQuery(opus, "bitrate=128")
	if err != nil {
		t.Fatalf("opus refused a bitrate: %v", err)
	}
	if parsed.Bitrate != 128 {
		t.Errorf("the bitrate is %d, want 128", parsed.Bitrate)
	}
	for _, path := range []string{
		"/v1/audio/sinks/kitchen/audio.wav",
		"/v1/audio/sinks/kitchen/audio.flac",
	} {
		route, _, _ := matchRoute(path)
		if _, err := parseCaptureQuery(route, "bitrate=128"); err == nil {
			t.Errorf("%s accepted a bitrate, want a refusal because only Opus accepts one", path)
		}
	}
}

func TestADocumentRouteTakesNoQueryParameters(t *testing.T) {
	route, _, _ := matchRoute("/v1/audio")
	if _, err := parseCaptureQuery(route, "t=0,5"); err == nil {
		t.Error("the discovery document accepted a span, want a refusal")
	}
	if _, err := parseCaptureQuery(route, ""); err != nil {
		t.Errorf("the discovery document refused an empty query: %v", err)
	}
}

// The round trip that plans/completed/09-capture-audio-over-http.md
// specifies: expand the template that the discovery document
// publishes, then send the result to the router.
func TestThePublishedTemplateExpandsIntoACallableQuery(t *testing.T) {
	template := aspectTemplate("sinks", "audio")
	if template != "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}" {
		t.Fatalf("the published template is %q, want %q", template, "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}")
	}
	// RFC 6570 form-style query expansion percent-encodes the comma,
	// which gives the t=10%2C20 form tested above.
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
		t.Errorf("the expansion named %q, want kitchen", name)
	}
	parsed, err := parseCaptureQuery(route, query)
	if err != nil {
		t.Fatalf("the expansion did not parse: %v", err)
	}
	if parsed.Span.Begin != 5*time.Second || parsed.Span.End != 7*time.Second || parsed.Bitrate != 128 {
		t.Errorf("the expansion parsed as %+v, want a span from 5s to 7s and a bitrate of 128", parsed)
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
		t.Errorf("an open span bounds the body at %d bytes, want 0 for no bound", got)
	}
}
