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
	"testing/synctest"
	"time"
)

// captureHarness serves a captureServer with fake pw-dump, pw-record,
// and encoder programs on the PATH, and a fake API server for the
// TokenReview. The fake pw-record writes the raw samples in the file
// samples, and the fakes write their arguments to args and encoder.
type captureHarness struct {
	server  *captureServer
	serving *httptest.Server
	samples string
	args    string
	encoder string

	mu    sync.Mutex
	lines []string
}

func (h *captureHarness) logged() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

// newCaptureHarness puts the fake programs in testdata/capture on the
// PATH, and the fake pw-dump returns the fixture named by graph.
func newCaptureHarness(t *testing.T, graph string, samples []byte) *captureHarness {
	t.Helper()
	work := t.TempDir()
	sampleFile := filepath.Join(work, "samples.raw")
	if err := os.WriteFile(sampleFile, samples, 0o600); err != nil {
		t.Fatalf("writing the samples file: %v", err)
	}
	bin, err := filepath.Abs("testdata/capture/bin")
	if err != nil {
		t.Fatalf("finding the fake binaries: %v", err)
	}
	graphFile, err := filepath.Abs("testdata/capture/" + graph)
	if err != nil {
		t.Fatalf("finding the graph fixture: %v", err)
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

// endsWithTheSamples makes the fake pw-record exit when the samples
// run out instead of looping, as a pw-record that dies during a tap.
func (h *captureHarness) endsWithTheSamples(t *testing.T) {
	t.Helper()
	t.Setenv("CAPTURE_FAKE_ONCE", "yes")
}

// linksTo sets the node id that the fake graph links the tap's stream
// to, so one fixture serves a tap on any endpoint in it.
func (h *captureHarness) linksTo(t *testing.T, nodeID string) {
	t.Helper()
	t.Setenv("CAPTURE_FAKE_TARGET", nodeID)
}

// call sends one request with the audio API's credential.
func (h *captureHarness) call(t *testing.T, method, target string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.serving.URL+target, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	response, err := h.serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// silence returns zeroed s16le samples for the duration, which is what
// a tap on a suspended sink delivers.
func silence(seconds float64, rate, channels int) []byte {
	return make([]byte, int(seconds*float64(rate*channels*sampleBytes)))
}

func TestATapRunsPwRecordWithTheLineThePlanStates(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.5")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	// The WAV header, then 0.5 s of samples at 48000 Hz, 2 channels,
	// and 2 bytes a sample.
	if len(body) != wavHeaderBytes+96000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+96000)
	}
	if string(body[:4]) != "RIFF" {
		t.Errorf("the body starts %q, want RIFF", body[:4])
	}

	args, err := os.ReadFile(harness.args)
	if err != nil {
		t.Fatalf("reading what pw-record was given: %v", err)
	}
	// The stream name contains the request ID, which varies, so the
	// test compares the text before it and after it.
	given := string(args)
	if !strings.HasPrefix(given, "-P\n{ node.name = \"audio-capture-") {
		t.Errorf("pw-record was given\n%q\nwant a line that starts with the stream's node.name", given)
	}
	tail := "\", stream.capture.sink = true }\n--target\n" +
		"liken-1-usb-0573-1573-a34004801402-usb-audio\n--raw\n--format\ns16\n" +
		"--rate\n48000\n--channels\n2\n-\n"
	if !strings.HasSuffix(given, tail) {
		t.Errorf("pw-record was given\n%q\nwant a line ending\n%q", given, tail)
	}
}

func TestATapOnASourceOmitsTheSinkProperty(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.5, 48000, 1))
	harness.linksTo(t, "47")
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sources/liken-1-usb-0573-1573-a34004801402-usb-audio-capture/audio.wav?t=0,0.25")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	args, _ := os.ReadFile(harness.args)
	if strings.Contains(string(args), "stream.capture.sink") {
		t.Errorf("a source tap carried the sink property: %q", args)
	}
	if !strings.Contains(string(args), "--channels\n1\n") {
		t.Errorf("the source's own channel count did not reach pw-record: %q", args)
	}
}

func TestASpanDiscardsAndThenDelivers(t *testing.T) {
	// At 48000 Hz, 2 channels, and s16le, one second is 192,000 bytes.
	// The span t=0.25,0.5 discards the first 48,000 bytes and delivers
	// the next 48,000.
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0.25,0.5")
	body, _ := io.ReadAll(response.Body)
	if len(body) != wavHeaderBytes+48000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+48000)
	}
}

func TestAnEncoderGetsTheEndpointsFormatAndTheMD5WarningIsNotAFailure(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.flac?t=0,0.125")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	if got := response.Header.Get("Content-Type"); got != "audio/flac" {
		t.Errorf("the Content-Type is %q, want audio/flac", got)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.HasPrefix(string(body), "fLaC") {
		t.Errorf("the body is %d bytes and starts %q, want fLaC", len(body), body[:min(len(body), 4)])
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

func TestOpusPassesTheBitrateParameterToTheEncoder(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.opus?t=0,0.125&bitrate=128")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the tap returned %s, want 200 OK: %s", response.Status, body)
	}
	if got := response.Header.Get("Content-Type"); got != "audio/ogg; codecs=opus" {
		t.Errorf("the Content-Type is %q, want audio/ogg; codecs=opus", got)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	args, _ := os.ReadFile(harness.encoder)
	if !strings.Contains(string(args), "--bitrate\n128\n") {
		t.Errorf("the bitrate did not reach opusenc: %q", args)
	}
}

func TestATapCarriesTheCaptureOnlyHeadersAndNothingTheAPIAdds(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.125")
	_, _ = io.Copy(io.Discard, response.Body)
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the container says Cache-Control: %q, want no-store", got)
	}
	if got := response.Header.Get("Accept-Ranges"); got != "none" {
		t.Errorf("the container says Accept-Ranges: %q, want none", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the container says X-Content-Type-Options: %q, want nosniff", got)
	}
	// The API adds these headers, so the container must not send them.
	for _, added := range []string{"Vary", "Link", "Content-Location", "Content-Disposition"} {
		if got := response.Header.Get(added); got != "" {
			t.Errorf("the container sent %s: %q, want no %s header", added, got, added)
		}
	}
}

func TestAHeadTakesNoSampleAndStartsNoProcess(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	response := harness.call(t, http.MethodHead,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the HEAD returned %s, want 200 OK", response.Status)
	}
	if got := response.Header.Get("Content-Type"); got != "audio/wav" {
		t.Errorf("the HEAD says Content-Type: %q, want audio/wav", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the HEAD says X-Content-Type-Options: %q, want nosniff", got)
	}
	body, _ := io.ReadAll(response.Body)
	if len(body) != 0 {
		t.Errorf("the HEAD body is %d bytes, want 0", len(body))
	}
	if _, err := os.Stat(harness.args); err == nil {
		t.Error("the HEAD started pw-record")
	}
}

func TestANameMissingFromTheGraphIsANotFound(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/kitchen/audio.wav")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown name returned %s, want 404 Not Found", response.Status)
	}
	document := readProblemBody(t, response)
	if document.Type != problemNoNode {
		t.Errorf("the problem type is %q, want %q", document.Type, problemNoNode)
	}
	if !strings.Contains(document.Detail, "kitchen") {
		t.Errorf("the detail is %q, want it to name kitchen", document.Detail)
	}
	if !strings.HasPrefix(document.Instance, "/v1/audio/sinks/kitchen/audio.wav#") {
		t.Errorf("the instance is %q, want the path followed by #", document.Instance)
	}
}

func TestAStreamThatLandsOnAnotherNodeIsAWrongTarget(t *testing.T) {
	// In this graph fixture the stream links to the HDMI sink, not to
	// the DAC that the request named.
	harness := newCaptureHarness(t, "graph-wrong-target.json", silence(1, 44100, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("a wrong target returned %s, want 500 Internal Server Error: %s", response.Status, body)
	}
	document := readProblemBody(t, response)
	if document.Type != problemWrongTarget {
		t.Errorf("the problem type is %q, want %q", document.Type, problemWrongTarget)
	}
}

func TestAQueryTheGrammarRefusesIsABadRequest(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	for _, target := range []string{
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=7,5",
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=2&t=10",
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=61",
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?bitrate=128",
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?width=480",
	} {
		response := harness.call(t, http.MethodGet, target)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s returned %s, want 400 Bad Request", target, response.Status)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func TestTheFifthTapIsRefusedWithARetryAfter(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	// Fill the tap limit, so the next request is over it.
	for range cap(harness.server.taps) {
		harness.server.taps <- struct{}{}
	}
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the fifth tap returned %s, want 503 Service Unavailable", response.Status)
	}
	if got := response.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q, want 5", got)
	}
	document := readProblemBody(t, response)
	if document.Type != problemCaptureBusy {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureBusy)
	}
}

func TestHealthReadinessAndMetricsNeedNoToken(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/metrics": http.StatusOK,
		// The TLS directory has no certificate yet, so the container is
		// not ready.
		"/readyz": http.StatusServiceUnavailable,
	} {
		response, err := harness.serving.Client().Get(harness.serving.URL + path)
		if err != nil {
			t.Fatalf("requesting %s: %v", path, err)
		}
		if response.StatusCode != want {
			t.Errorf("%s returned %s, want %d", path, response.Status, want)
		}
		_ = response.Body.Close()
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
	response, err := serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("another ServiceAccount returned %s, want 401 Unauthorized", response.Status)
	}
	if got := response.Header.Get("WWW-Authenticate"); !strings.Contains(got, `error="invalid_token"`) {
		t.Errorf("the challenge is %q, want error=\"invalid_token\"", got)
	}
}

func TestARequestWithNoTokenIsChallengedWithTheRealmAlone(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	response, err := harness.serving.Client().Get(
		harness.serving.URL + "/v1/audio/sinks/kitchen/audio.wav")
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no token returned %s, want 401 Unauthorized", response.Status)
	}
	if got := response.Header.Get("WWW-Authenticate"); got != `Bearer realm="audio-capture"` {
		t.Errorf("the challenge is %q, want %q", got, `Bearer realm="audio-capture"`)
	}
}

func TestTheContainerServesNoNegotiatedRoute(t *testing.T) {
	// The API always forwards a path with a format extension, so the
	// container has no route for a path without one.
	harness := newCaptureHarness(t, "graph.json", nil)
	for _, target := range []string{
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio",
		"/v1/audio",
		"/v1/audio/openapi.json",
	} {
		response := harness.call(t, http.MethodGet, target)
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s returned %s, want 404 Not Found", target, response.Status)
		}
		_, _ = io.Copy(io.Discard, response.Body)
	}
}

func TestTheInfoRouteReturnsTheFormatFromTheGraph(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	response := harness.call(t, http.MethodGet, "/v1/audio/sinks/pci-0000-00-1f-3-hdmi-0")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("the info route returned %s, want 200 OK: %s", response.Status, body)
	}
	var format captureFormatDocument
	if err := json.NewDecoder(response.Body).Decode(&format); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if format.Node != "pci-0000-00-1f-3-hdmi-0" || format.Rate != 48000 || format.Channels != 6 {
		t.Errorf("the info route returned %+v, want node pci-0000-00-1f-3-hdmi-0 at 48000 Hz with 6 channels", format)
	}
	if _, err := os.Stat(harness.args); err == nil {
		t.Error("the info route started pw-record")
	}
}

func TestOptionsListsTheAllowedMethodsAndPostIsRefused(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", nil)
	response := harness.call(t, http.MethodOptions,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS returned %s, want 204 No Content", response.Status)
	}
	if got := response.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("OPTIONS says Allow: %q, want GET, HEAD, OPTIONS", got)
	}
	_ = response.Body.Close()

	response = harness.call(t, http.MethodPost,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST returned %s, want 405 Method Not Allowed", response.Status)
	}
	if got := response.Header.Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("a 405 says Allow: %q, want GET, HEAD, OPTIONS", got)
	}
	_, _ = io.Copy(io.Discard, response.Body)
}

func TestASuspendedSinkTapsAsSilence(t *testing.T) {
	// The HDMI sink in the fixture has no Format, so the container
	// reads the six channels from EnumFormat and the rate from the
	// PipeWire settings metadata. A suspended sink is not a failure, so
	// the tap returns 200 with silence.
	harness := newCaptureHarness(t, "graph.json", silence(0.125, 48000, 6))
	harness.linksTo(t, "48")
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,0.0625")
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("a suspended sink returned %s, want 200 OK: %s", response.Status, body)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	args, _ := os.ReadFile(harness.args)
	if !strings.Contains(string(args), "--channels\n6\n") {
		t.Errorf("a suspended 5.1 sink was tapped as %q, want --channels 6", args)
	}
}

func TestConfirmReadsTheGraphUntilTheLinkDeadline(t *testing.T) {
	// confirm reads the graph again until the stream links to the
	// target or linkDeadline passes. A graph with no link runs to the
	// deadline.
	synctest.Test(t, func(t *testing.T) {
		server := &captureServer{
			version:  "dev",
			readings: newCaptureMetrics("dev"),
			taps:     make(chan struct{}, 1),
			now:      time.Now,
		}
		reads := 0
		server.graph = func(context.Context) ([]byte, error) {
			reads++
			return readGraphFixture(t, "graph-no-settings.json"), nil
		}
		started := time.Now()
		err := server.confirm(t.Context(), "audio-capture-4242", 48)
		if err == nil {
			t.Fatal("a graph with no link at all confirmed")
		}
		if waited := time.Since(started); waited != linkDeadline {
			t.Errorf("the confirmation gave up after %s, want %s", waited, linkDeadline)
		}
		if reads < 2 {
			t.Errorf("the confirmation read the graph %d times, want at least 2", reads)
		}
	})
}

func readProblemBody(t *testing.T, response *http.Response) problem {
	t.Helper()
	if got := response.Header.Get("Content-Type"); got != problemType {
		t.Errorf("the problem is served as %q, want %q", got, problemType)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the problem body: %v", err)
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
	// A value that is not a positive integer gives the default.
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

// When pw-dump fails after the tap starts, the container cannot confirm
// where the stream linked. It returns a 503 that the caller can retry,
// and not a 500 wrong-target, because PipeWire restarts on its own.
func TestAPipeWireThatStopsRespondingIsUnavailableAndNotAWrongTarget(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	// The first graph read succeeds and the later reads fail, as when
	// the daemon dies after the container found the node.
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
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	if response.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("a dead PipeWire returned %s, want 503 Service Unavailable: %s", response.Status, body)
	}
	if got := response.Header.Get("Retry-After"); got != "5" {
		t.Errorf("the refusal says Retry-After: %q, want 5", got)
	}
	document := readProblemBody(t, response)
	if document.Type == problemWrongTarget {
		t.Error("a dead PipeWire was called a wrong target")
	}
	// The detail includes the text of the failed graph read.
	if !strings.Contains(document.Detail, "can't connect: Host is down") {
		t.Errorf("the detail is %q, want the daemon's own text, can't connect: Host is down", document.Detail)
	}
}

func TestAGraphThatWillNotReadIsNotAWrongTarget(t *testing.T) {
	server := &captureServer{
		version:  "dev",
		readings: newCaptureMetrics("dev"),
		taps:     make(chan struct{}, 1),
		now:      time.Now,
	}
	server.graph = func(context.Context) ([]byte, error) {
		return nil, fmt.Errorf("%w: running pw-dump: exit status 255", ErrGraphUnread)
	}
	err := server.confirm(context.Background(), "audio-capture-4242", 46)
	if !errors.Is(err, ErrGraphUnread) {
		t.Errorf("a graph that will not read returned %v, want ErrGraphUnread", err)
	}
}

// Every tap writes one log line, including a tap that fails the link
// confirmation, as
// audio-operator/plans/completed/09-capture-audio-over-http.md
// requires.
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
		response := harness.call(t, http.MethodGet,
			"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/"+row.target)
		_, _ = io.Copy(io.Discard, response.Body)

		lines := harness.logged()
		if len(lines) != 1 {
			t.Errorf("%s wrote %d lines, want 1: %v", row.name, len(lines), lines)
			continue
		}
		line := lines[0]
		for _, want := range []string{
			"target=liken-1-usb-0573-1573-a34004801402-usb-audio",
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
				t.Errorf("%s wrote %q, want a line that contains %s", row.name, line, want)
			}
		}
		// The line has the request ID, so a person can match it with the
		// API's log line for the same request.
		if !strings.Contains(line, "tap ") {
			t.Errorf("%s wrote %q, want a line that names the request after tap", row.name, line)
		}
	}
}

func TestATapRefusedByTheLimitWritesNoTapLine(t *testing.T) {
	// No tap started, so there is no tap line. The metrics and the
	// API's log line record the refusal.
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	for range cap(harness.server.taps) {
		harness.server.taps <- struct{}{}
	}
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav")
	_, _ = io.Copy(io.Discard, response.Body)
	if lines := harness.logged(); len(lines) != 0 {
		t.Errorf("a refused tap wrote %v, want no line", lines)
	}
}

// When pw-record or the encoder dies after the container sent the 200
// and the first bytes, the status cannot change. The container ends the
// body without the terminating chunk, which RFC 9112 section 8 defines
// as an incomplete message, so the client's read fails and the body is
// not mistaken for a complete recording.
func TestAPipelineThatDiedMidTapEndsTheBodyIncomplete(t *testing.T) {
	// The samples end before the span, so the fake pw-record exits
	// early.
	harness := newCaptureHarness(t, "graph.json", silence(0.05, 48000, 2))
	harness.endsWithTheSamples(t)

	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,5")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the tap returned %s, want 200 OK", response.Status)
	}
	_, err := io.ReadAll(response.Body)
	if err == nil {
		t.Fatal("a capture that was cut short ended as though it were complete")
	}

	lines := harness.logged()
	if len(lines) != 1 || !strings.Contains(lines[0], "truncated") {
		t.Errorf("the container logged %v, want one line that contains truncated", lines)
	}
}

func TestAFinishedSpanEndsCleanly(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	response := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.125")
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading a finished span returned %v, want a clean end", err)
	}
	// 0.125 s at 48000 Hz, two channels, two bytes a sample.
	if len(body) != wavHeaderBytes+24000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+24000)
	}
	lines := harness.logged()
	if len(lines) != 1 || strings.Contains(lines[0], "truncated") {
		t.Errorf("a finished span logged %v, want one line without truncated", lines)
	}
}

func TestAClientThatHungUpEndsCleanly(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	// The request has no end time. The client reads one block and then
	// closes the body.
	request, err := http.NewRequest(http.MethodGet,
		harness.serving.URL+"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	response, err := harness.serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	if _, err := io.ReadFull(response.Body, make([]byte, 64)); err != nil {
		t.Fatalf("reading the first block: %v", err)
	}
	_ = response.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(harness.logged()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	lines := harness.logged()
	if len(lines) != 1 {
		t.Fatalf("the container logged %v, want one line", lines)
	}
	if strings.Contains(lines[0], "truncated") {
		t.Errorf("a client that hung up was read as a truncation: %q", lines[0])
	}
}

// The container sends the response headers before the first sample of
// the span. A tap with a begin offset discards audio until that offset,
// so headers that waited for audio would leave the caller with no
// response for the length of the offset.
func TestTheContainerSendsItsHeadersBeforeTheDiscardIsOver(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(1, 48000, 2))
	// The fake encoder writes nothing until it receives samples, like a
	// real one, so only a flush of the headers sends them before the
	// clock reaches begin.
	t.Setenv("CAPTURE_FAKE_ENCODER_QUIET", "yes")

	request, err := http.NewRequest(http.MethodGet, harness.serving.URL+
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.flac?t=0.4,0.5", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	started := time.Now()
	response, err := harness.serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	waited := time.Since(started)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("the tap returned %s, want 200 OK", response.Status)
	}
	if waited > 200*time.Millisecond {
		t.Errorf("the headers took %s, want under 200 ms, before the clock reaches the begin at 400 ms", waited)
	}
	if got := response.Header.Get("Content-Type"); got != "audio/flac" {
		t.Errorf("the Content-Type is %q, want audio/flac", got)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	if total := time.Since(started); total < 400*time.Millisecond {
		t.Errorf("the whole tap took %s, want at least the 400 ms before begin", total)
	}
}

// A span that finishes normally ends with the terminating chunk. If the
// container treated a normal finish as a cut-short capture, an HTTP/2
// client would read the whole Opus body and then fail with
// INTERNAL_ERROR. The container drains the encoder instead of killing
// it, so the encoder exits with status 0. It also does not count as a
// failure the exec.ErrWaitDelay that os/exec returns when a process
// exits normally while its pipes are still open.
func TestAFinishedOpusSpanEndsCleanlyOverHTTP2(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(2, 48000, 2))
	// Like a real encoder, the fake writes its last page a moment after
	// its stdin closes.
	t.Setenv("CAPTURE_FAKE_ENCODER_LINGER", "0.1")

	// Serve over HTTP/2, because that is where the client sees the
	// stream reset.
	serving := httptest.NewUnstartedServer(harness.server.handler())
	serving.EnableHTTP2 = true
	serving.StartTLS()
	t.Cleanup(serving.Close)

	request, err := http.NewRequest(http.MethodGet, serving.URL+
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.opus?t=0,0.25", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	response, err := serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Proto != "HTTP/2.0" {
		t.Fatalf("the test spoke %s, want HTTP/2.0, because only an HTTP/2 client reports a cut body as INTERNAL_ERROR", response.Proto)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the tap returned %s, want 200 OK", response.Status)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading a finished span returned %v, want a clean end", err)
	}
	// 0.25 s at 48000 Hz, 2 channels, and 2 bytes a sample, after the
	// 4-byte marker that the fake encoder writes first.
	if len(body) != 4+48000 {
		t.Errorf("the body is %d bytes, want %d", len(body), 4+48000)
	}

	lines := harness.logged()
	if len(lines) != 1 {
		t.Fatalf("the container logged %v, want one line", lines)
	}
	if strings.Contains(lines[0], "truncated") {
		t.Errorf("a finished span was read as cut short: %q", lines[0])
	}
	// The encoder's exit status 0 shows that the container waited for
	// it and did not kill it.
	if !strings.Contains(lines[0], "encoder:0") {
		t.Errorf("the encoder was not waited for: %q", lines[0])
	}
}

func TestAFinishedWAVSpanEndsCleanlyOverHTTP2(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(2, 48000, 2))
	serving := httptest.NewUnstartedServer(harness.server.handler())
	serving.EnableHTTP2 = true
	serving.StartTLS()
	t.Cleanup(serving.Close)

	request, err := http.NewRequest(http.MethodGet, serving.URL+
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.wav?t=0,0.25", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer a.b.c")
	response, err := serving.Client().Do(request)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading a finished span returned %v, want a clean end", err)
	}
	if len(body) != wavHeaderBytes+48000 {
		t.Errorf("the body is %d bytes, want %d", len(body), wavHeaderBytes+48000)
	}
}
