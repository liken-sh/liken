package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureHarness is one capture container under test: the fake
// pw-dump, pw-record, and encoders on the path, a fake API server for
// the TokenReview, and the samples the tap delivers.
type captureHarness struct {
	server  *captureServer
	serving *httptest.Server
	samples string
	args    string
	encoder string

	mu    sync.Mutex
	lines []string
}

// logged is the per-tap lines this container wrote.
func (h *captureHarness) logged() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

// newCaptureHarness points the container at the fixtures in
// testdata/capture and gives it a graph to read.
func newCaptureHarness(t *testing.T, graph string, samples []byte) *captureHarness {
	t.Helper()
	work := t.TempDir()
	sampleFile := filepath.Join(work, "samples.raw")
	if err := os.WriteFile(sampleFile, samples, 0o600); err != nil {
		t.Fatal(err)
	}
	bin, err := filepath.Abs("testdata/capture/bin")
	if err != nil {
		t.Fatal(err)
	}
	graphFile, err := filepath.Abs("testdata/capture/" + graph)
	if err != nil {
		t.Fatal(err)
	}
	harness := &captureHarness{
		samples: sampleFile,
		args:    filepath.Join(work, "pw-record.args"),
		encoder: filepath.Join(work, "encoder.args"),
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE_FAKE_ARGS", harness.args)
	t.Setenv("CAPTURE_FAKE_ENCODER_ARGS", harness.encoder)
	t.Setenv("CAPTURE_FAKE_SAMPLES", sampleFile)
	t.Setenv("CAPTURE_FAKE_GRAPH", graphFile)
	t.Setenv("CAPTURE_FAKE_TARGET", "46")

	review := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{captureAudience},
		User:          tokenUserInfo{Username: captureCaller},
	})
	harness.server = newCaptureServer(newCaptureMetrics("dev"), newLeaf(work),
		review.reviewer(captureAudience), 4)
	harness.server.log = func(line string) {
		harness.mu.Lock()
		defer harness.mu.Unlock()
		harness.lines = append(harness.lines, line)
	}
	harness.serving = httptest.NewServer(harness.server.handler())
	t.Cleanup(harness.serving.Close)
	return harness
}

// linksTo says which node the fixture's link lands on, which is how
// one graph serves a tap on any endpoint in it.
func (h *captureHarness) linksTo(t *testing.T, nodeID string) {
	t.Helper()
	t.Setenv("CAPTURE_FAKE_TARGET", nodeID)
}

// call makes one request with the API's own credential.
func (h *captureHarness) call(t *testing.T, method, target string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.serving.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	answer, err := h.serving.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = answer.Body.Close() })
	return answer
}

// silence is one second of s16le at 48000 Hz and two channels, which
// is what a tap on a suspended sink delivers.
func silence(seconds float64, rate, channels int) []byte {
	return make([]byte, int(seconds*float64(rate*channels*sampleBytes)))
}

func TestATapRunsPwRecordWithTheLineThePlanStates(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.5")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	// The 44-byte header, then half a second of samples: 48,000 Hz,
	// two channels, two bytes a sample.
	if len(body) != wavHeaderBytes+96000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+96000)
	}
	if string(body[:4]) != "RIFF" {
		t.Errorf("the body starts %q", body[:4])
	}

	args, err := os.ReadFile(harness.args)
	if err != nil {
		t.Fatalf("reading what pw-record was given: %v", err)
	}
	// The stream's own name is the request's, so the line is compared
	// around it.
	given := string(args)
	if !strings.HasPrefix(given, "-P\n{ node.name = \"audio-capture-") {
		t.Errorf("pw-record was given\n%q", given)
	}
	tail := "\", stream.capture.sink = true }\n--target\n" +
		"usb-0573-1573-a34004801402-usb-audio\n--raw\n--format\ns16\n" +
		"--rate\n48000\n--channels\n2\n-\n"
	if !strings.HasSuffix(given, tail) {
		t.Errorf("pw-record was given\n%q\nwant a line ending\n%q", given, tail)
	}
}

func TestATapOnASourceOmitsTheSinkProperty(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.5, 48000, 1))
	harness.linksTo(t, "47")
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sources/usb-0573-1573-a34004801402-usb-audio-capture/audio.wav?t=0,0.25")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	args, _ := os.ReadFile(harness.args)
	if strings.Contains(string(args), "stream.capture.sink") {
		t.Errorf("a source tap carried the sink property: %q", args)
	}
	if !strings.Contains(string(args), "--channels\n1\n") {
		t.Errorf("the source's own channel count did not reach pw-record: %q", args)
	}
}

func TestASpanDiscardsAndThenDelivers(t *testing.T) {
	// 48000 Hz, two channels, s16le: 192,000 bytes a second. t=0.25,0.5
	// discards 48,000 bytes and delivers 48,000.
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0.25,0.5")
	body, _ := io.ReadAll(answer.Body)
	if len(body) != wavHeaderBytes+48000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+48000)
	}
}

func TestAnEncoderGetsTheEndpointsFormatAndTheMD5WarningIsNotAFailure(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.flac?t=0,0.125")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	if got := answer.Header.Get("Content-Type"); got != "audio/flac" {
		t.Errorf("the type is %q", got)
	}
	body, _ := io.ReadAll(answer.Body)
	if string(body[:4]) != "fLaC" {
		t.Errorf("the body starts %q", body[:4])
	}
	args, err := os.ReadFile(harness.encoder)
	if err != nil {
		t.Fatalf("reading what flac was given: %v", err)
	}
	for _, want := range []string{"--force-raw-format", "--bps=16", "--channels=2", "--sample-rate=48000"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("flac was not given %s: %q", want, args)
		}
	}
}

func TestOpusCarriesTheBitrateKnobThroughToTheEncoder(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.opus?t=0,0.125&bitrate=128")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the tap answered %s: %s", answer.Status, body)
	}
	if got := answer.Header.Get("Content-Type"); got != "audio/ogg; codecs=opus" {
		t.Errorf("the type is %q", got)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	args, _ := os.ReadFile(harness.encoder)
	if !strings.Contains(string(args), "--bitrate\n128\n") {
		t.Errorf("the bitrate did not reach opusenc: %q", args)
	}
}

func TestATapCarriesTheCaptureOnlyHeadersAndNothingTheAPIAdds(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.125")
	_, _ = io.Copy(io.Discard, answer.Body)
	if got := answer.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the container says Cache-Control: %q", got)
	}
	if got := answer.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("the container says Accept-Ranges: %q", got)
	}
	// The API adds these four; the container sends none of them.
	for _, added := range []string{"Vary", "Link", "Content-Location", "Content-Disposition"} {
		if got := answer.Header.Get(added); got != "" {
			t.Errorf("the container sent %s: %q", added, got)
		}
	}
}

func TestAHeadTakesNoSampleAndStartsNoProcess(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	answer := harness.call(t, http.MethodHead,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the HEAD answered %s", answer.Status)
	}
	if got := answer.Header.Get("Content-Type"); got != "audio/wav" {
		t.Errorf("the HEAD says Content-Type: %q", got)
	}
	body, _ := io.ReadAll(answer.Body)
	if len(body) != 0 {
		t.Errorf("the HEAD carried %d bytes", len(body))
	}
	if _, err := os.Stat(harness.args); err == nil {
		t.Error("the HEAD started pw-record")
	}
}

func TestANameTheGraphDoesNotHoldIsANotFound(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav")
	if answer.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown name answered %s", answer.Status)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemNoNode {
		t.Errorf("the problem type is %q", document.Type)
	}
	if !strings.Contains(document.Detail, "kitchen") {
		t.Errorf("the detail is %q, and it must carry what was asked for", document.Detail)
	}
	if !strings.HasPrefix(document.Instance, "/v1/audio/sinks/kitchen/audio.wav#") {
		t.Errorf("the instance is %q", document.Instance)
	}
}

func TestAStreamThatLandsOnAnotherNodeIsAWrongTarget(t *testing.T) {
	// The graph fixture links the stream to the HDMI sink, not to the
	// DAC the request named.
	harness := newCaptureHarness(t, "graph-wrong-target.json", silence(1, 44100, 2))
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("a wrong target answered %s: %s", answer.Status, body)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemWrongTarget {
		t.Errorf("the problem type is %q, want %q", document.Type, problemWrongTarget)
	}
}

func TestAQueryTheGrammarRefusesIsABadRequest(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	for _, target := range []string{
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=7,5",
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=2&t=10",
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?t=61",
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?bitrate=128",
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav?width=480",
	} {
		answer := harness.call(t, http.MethodGet, target)
		if answer.StatusCode != http.StatusBadRequest {
			t.Errorf("%s answered %s", target, answer.Status)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

func TestTheFifthTapIsRefusedWithARetryAfter(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	// Four slots are already held, so the next request is the fifth.
	for range cap(harness.server.taps) {
		harness.server.taps <- struct{}{}
	}
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the fifth tap answered %s", answer.Status)
	}
	if got := answer.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q, want 5", got)
	}
	document := readProblemBody(t, answer)
	if document.Type != problemCaptureBusy {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureBusy)
	}
}

func TestTheThreeUntokenedRoutesNeedNoToken(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/metrics": http.StatusOK,
		// No leaf has been minted into the directory, so the container
		// is not ready and says so.
		"/readyz": http.StatusServiceUnavailable,
	} {
		answer, err := harness.serving.Client().Get(harness.serving.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if answer.StatusCode != want {
			t.Errorf("%s answered %s, want %d", path, answer.Status, want)
		}
		_ = answer.Body.Close()
	}
}

func TestOnlyTheAPIsServiceAccountReachesATap(t *testing.T) {
	work := t.TempDir()
	review := newReviewServer(t, tokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{captureAudience},
		User:          tokenUserInfo{Username: "system:serviceaccount:default:someone-else"},
	})
	server := newCaptureServer(newCaptureMetrics("dev"), newLeaf(work),
		review.reviewer(captureAudience), 4)
	serving := httptest.NewServer(server.handler())
	t.Cleanup(serving.Close)

	request, _ := http.NewRequest(http.MethodGet,
		serving.URL+"/v1/audio/sinks/kitchen/audio.wav", nil)
	request.Header.Set("Authorization", "Bearer a.b.c")
	answer, err := serving.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("another ServiceAccount answered %s", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); !strings.Contains(got, `error="invalid_token"`) {
		t.Errorf("the challenge is %q", got)
	}
}

func TestARequestWithNoTokenIsChallengedWithTheRealmAlone(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	answer, err := harness.serving.Client().Get(
		harness.serving.URL + "/v1/audio/sinks/kitchen/audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = answer.Body.Close() }()
	if answer.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no token answered %s", answer.Status)
	}
	if got := answer.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-capture"` {
		t.Errorf("the challenge is %q", got)
	}
}

func TestTheContainerServesNoNegotiatedRoute(t *testing.T) {
	// The container's route mirrors the public one with the extension
	// always present, so the API never forwards an extensionless path.
	harness := newCaptureHarness(t, "graph.json", nil)
	for _, target := range []string{
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio",
		"/v1/audio",
		"/v1/audio/openapi.json",
	} {
		answer := harness.call(t, http.MethodGet, target)
		if answer.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %s", target, answer.Status)
		}
		_, _ = io.Copy(io.Discard, answer.Body)
	}
}

func TestTheContainerAnswersTheInfoRouteWithTheGraphsOwnFormat(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	answer := harness.call(t, http.MethodGet, "/v1/audio/sinks/pci-0000-00-1f-3-hdmi-0")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("the info route answered %s: %s", answer.Status, body)
	}
	var format captureFormatDocument
	if err := json.NewDecoder(answer.Body).Decode(&format); err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if format.Node != "pci-0000-00-1f-3-hdmi-0" || format.Rate != 48000 || format.Channels != 6 {
		t.Errorf("the info route answered %+v", format)
	}
	if _, err := os.Stat(harness.args); err == nil {
		t.Error("the info route started pw-record")
	}
}

func TestOptionsAnswersTheThreeMethodsAndAnythingElseIsRefused(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	answer := harness.call(t, http.MethodOptions,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS answered %s", answer.Status)
	}
	if got := answer.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("OPTIONS says Allow: %q", got)
	}
	_ = answer.Body.Close()

	answer = harness.call(t, http.MethodPost,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST answered %s", answer.Status)
	}
	if got := answer.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("a 405 says Allow: %q", got)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
}

func TestASuspendedSinkTapsAsSilence(t *testing.T) {
	// The HDMI sink in the fixture reports no Format, so the tap takes
	// six channels from EnumFormat and the rate from the settings
	// metadata, and answers 200 rather than reporting a failure the
	// service does not have.
	harness := newCaptureHarness(t, "graph.json", silence(0.125, 48000, 6))
	harness.linksTo(t, "48")
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,0.0625")
	if answer.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("a suspended sink answered %s: %s", answer.Status, body)
	}
	_, _ = io.Copy(io.Discard, answer.Body)
	args, _ := os.ReadFile(harness.args)
	if !strings.Contains(string(args), "--channels\n6\n") {
		t.Errorf("a suspended 5.1 sink was tapped as %q", args)
	}
}

func TestTheGraphIsReadBeforeTheTapAndAgainToConfirm(t *testing.T) {
	// The resolution is one read and the confirmation is at least one
	// more, so a fixture that never links reaches the deadline and a
	// wrong-target answer.
	server := &captureServer{
		version:  "dev",
		readings: newCaptureMetrics("dev"),
		taps:     make(chan struct{}, 1),
		now:      time.Now,
	}
	reads := 0
	server.linkDeadline = 300 * time.Millisecond
	server.graph = func(context.Context) ([]byte, error) {
		reads++
		return readGraphFixture(t, "graph-no-settings.json"), nil
	}
	err := server.confirm(context.Background(), "audio-capture-4242", 48)
	if err == nil {
		t.Fatal("a graph with no link at all confirmed")
	}
	if reads < 2 {
		t.Errorf("the confirmation read the graph %d times", reads)
	}
}

func readProblemBody(t *testing.T, answer *http.Response) problem {
	t.Helper()
	if got := answer.Header.Get("Content-Type"); got != problemType {
		t.Errorf("the problem is served as %q, want %q", got, problemType)
	}
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatal(err)
	}
	var document problem
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("the body is not a problem document: %v: %s", err, body)
	}
	return document
}

func TestTheTapLimitComesFromTheEnvironment(t *testing.T) {
	t.Setenv(captureTapsVariable, "8")
	if got := captureTapLimit(); got != 8 {
		t.Errorf("the limit is %d, want 8", got)
	}
	// A value the container cannot read, and a value that would serve
	// nothing, both fall back to the default rather than parking the
	// node with no taps at all.
	for _, value := range []string{"", "many", "0", "-1"} {
		t.Setenv(captureTapsVariable, value)
		if got := captureTapLimit(); got != defaultCaptureTaps {
			t.Errorf("CAPTURE_TAPS=%q gave the limit %d, want %d",
				value, got, defaultCaptureTaps)
		}
	}
}

func TestTheSpanTheLogLineNames(t *testing.T) {
	cases := map[string]timeSpan{
		"open":     {},
		"5s":       {Begin: 5 * time.Second},
		"5s,7s":    {Begin: 5 * time.Second, End: 7 * time.Second, Ended: true},
		"0s,500ms": {End: 500 * time.Millisecond, Ended: true},
	}
	for want, span := range cases {
		if got := spanWords(span); got != want {
			t.Errorf("the span reads %q, want %q", got, want)
		}
	}
}

// A PipeWire that answers nothing says nothing about where the tap
// landed. The drill killed the daemon mid-tap and got a 500
// wrong-target with "can't connect: Host is down" in it; a daemon that
// is down clears on its own, so it is a 503.
func TestAPipeWireThatDoesNotAnswerIsUnavailableAndNotAWrongTarget(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	// The graph resolves once, then the daemon goes away, which is what
	// the confirmation meets.
	reads := 0
	resolve := harness.server.graph
	harness.server.graph = func(ctx context.Context) ([]byte, error) {
		reads++
		if reads == 1 {
			return resolve(ctx)
		}
		return nil, fmt.Errorf("%w: running pw-dump: exit status 255: can't connect: Host is down",
			ErrGraphUnread)
	}
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if answer.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(answer.Body)
		t.Fatalf("a dead PipeWire answered %s: %s", answer.Status, body)
	}
	if got := answer.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q", got)
	}
	document := readProblemBody(t, answer)
	if document.Type == problemWrongTarget {
		t.Error("a dead PipeWire was called a wrong target")
	}
	// The daemon's own words reach the caller.
	if !strings.Contains(document.Detail, "can't connect: Host is down") {
		t.Errorf("the detail is %q", document.Detail)
	}
}

func TestAGraphThatWillNotReadIsNotAWrongTarget(t *testing.T) {
	server := &captureServer{
		version:      "dev",
		readings:     newCaptureMetrics("dev"),
		taps:         make(chan struct{}, 1),
		now:          time.Now,
		linkDeadline: 300 * time.Millisecond,
	}
	server.graph = func(context.Context) ([]byte, error) {
		return nil, fmt.Errorf("%w: running pw-dump: exit status 255", ErrGraphUnread)
	}
	err := server.confirm(context.Background(), "audio-capture-4242", 46)
	if !errors.Is(err, ErrGraphUnread) {
		t.Errorf("a graph that will not read answered %v", err)
	}
}

// The plan asks for one line per tap in the container. The drill found
// none at all, because every tap ended at the confirmation and only
// the paths past it wrote one.
func TestEveryTapWritesOneLineWhateverBecameOfIt(t *testing.T) {
	cases := []struct {
		name   string
		target string
		graph  string
		link   string
		rate   string
	}{
		{"a tap that delivered", "audio.wav?t=0,0.125", "graph.json", "link=on-target", "rate=48000"},
		{"a tap that landed elsewhere", "audio.wav", "graph-wrong-target.json",
			"link=wrong-target", "rate=44100"},
	}
	for _, row := range cases {
		harness := newCaptureHarness(t, row.graph, silence(0.5, 48000, 2))
		harness.server.linkDeadline = 300 * time.Millisecond
		answer := harness.call(t, http.MethodGet,
			"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/"+row.target)
		_, _ = io.Copy(io.Discard, answer.Body)

		lines := harness.logged()
		if len(lines) != 1 {
			t.Errorf("%s wrote %d lines, want 1: %v", row.name, len(lines), lines)
			continue
		}
		line := lines[0]
		for _, want := range []string{
			"target=usb-0573-1573-a34004801402-usb-audio",
			"node=46",
			"stream=audio-capture-",
			"format=wav",
			row.rate,
			"channels=2",
			"span=",
			"bytes=",
			"discarded=",
			row.link,
			"ended=",
		} {
			if !strings.Contains(line, want) {
				t.Errorf("%s wrote %q, which carries no %s", row.name, line, want)
			}
		}
		// The line names the request, so it joins the API's own.
		if !strings.Contains(line, "tap ") {
			t.Errorf("%s names no request: %q", row.name, line)
		}
	}
}

func TestATapRefusedByTheLimitWritesNoTapLine(t *testing.T) {
	// Nothing was tapped, so there is nothing to say about a tap. The
	// refusal is on the metric and in the API's line.
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	for range cap(harness.server.taps) {
		harness.server.taps <- struct{}{}
	}
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav")
	_, _ = io.Copy(io.Discard, answer.Body)
	if lines := harness.logged(); len(lines) != 0 {
		t.Errorf("a refused tap wrote %v", lines)
	}
}
