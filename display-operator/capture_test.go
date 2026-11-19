package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// newCaptureFixture returns a sidecar with no compositor and no
// encoder.
func newCaptureFixture(t *testing.T) *captureServer {
	t.Helper()
	readings := newCaptureMetrics(captureComponent, version)
	return &captureServer{
		tokens: newTokenCache(func(token string) (*caller, *fault) {
			switch token {
			case "the-api-token":
				return &caller{Username: "system:serviceaccount:liken-system:display-api"}, nil
			case "somebody-elses-token":
				return &caller{Username: "system:serviceaccount:default:curious"}, nil
			}
			return nil, unauthenticated("the token is not valid for the audience display-capture")
		}),
		readings:   readings,
		process:    processHandler(readings.registry, func() bool { return true }),
		socketPath: t.TempDir() + "/wayland-capture",
		perOutput:  1,
		now:        time.Now,
		running:    map[string][]*runningCapture{},
	}
}

func askSidecar(t *testing.T, server *captureServer, method, target, token string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Result()
}

func TestTheMetricsAndProbePathsNeedNoToken(t *testing.T) {
	server := newCaptureFixture(t)
	for _, path := range []string{"/metrics", "/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			resp := askSidecar(t, server, http.MethodGet, path, "")
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s responded with %d with no token, want 200", path, resp.StatusCode)
			}
		})
	}
}

func TestTheSidecarServesItsOwnMetrics(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet, "/metrics", "")
	served := body(t, resp)
	for _, series := range []string{
		"display_capture_ready",
		"display_capture_bytes_total",
		"display_capture_seconds_total",
		"display_captures_active",
		"display_capture_frames_total",
		"display_capture_failures_total",
		"liken_build_info",
	} {
		if !strings.Contains(served, series) {
			t.Errorf("the scrape has no %s, want it", series)
		}
	}
}

func TestTheSidecarServesOnlyDisplayAPI(t *testing.T) {
	cases := []struct {
		name   string
		token  string
		status int
	}{
		{"no token at all", "", http.StatusUnauthorized},
		{"a token the review refuses", "a-strangers-token", http.StatusUnauthorized},
		{"a token of another account", "somebody-elses-token", http.StatusForbidden},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newCaptureFixture(t)
			resp := askSidecar(t, server, http.MethodGet,
				apiRoot+"/displays/HDMI-A-1/screen.png", row.token)
			if resp.StatusCode != row.status {
				t.Errorf("the sidecar responded with %d, want %d", resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != problemMediaType {
				t.Errorf("the refusal Content-Type is %q, want %q", got, problemMediaType)
			}
		})
	}
}

// A HEAD request reads no frame, so it returns 200 with no compositor
// on the socket.
func TestAHeadRequestReadsNoFrame(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodHead,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a HEAD responded with %d with no compositor, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("the HEAD Content-Type is %q, want image/png", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the HEAD X-Content-Type-Options is %q, want nosniff", got)
	}
}

func TestACompositorThatDoesNotAnswerGivesA503(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a dead compositor responded with %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q, want %q", got, retryAfterSeconds)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if !strings.Contains(document.Detail, "no such file or directory") {
		t.Errorf("the detail is %q, want it to contain the socket error \"no such file or directory\"", document.Detail)
	}
}

func TestOneCapturePerOutput(t *testing.T) {
	server := newCaptureFixture(t)
	release, f := server.take("HDMI-A-1", time.Time{})
	if f != nil {
		t.Fatalf("the first capture was refused: %v", f)
	}

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.mp4", "the-api-token")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a second capture responded with %d, want 503", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if document.Type != problemCaptureBusy {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureBusy)
	}
	if !strings.Contains(document.Detail, "until its client closes") {
		t.Errorf("the detail is %q, want it to say when the running capture ends", document.Detail)
	}

	release()
	if _, f := server.take("HDMI-A-1", time.Time{}); f != nil {
		t.Errorf("after the first capture ended, a second capture of the same output was refused: %v", f)
	}
	if _, f := server.take("HDMI-A-2", time.Time{}); f != nil {
		t.Errorf("a capture of a second output was refused: %v", f)
	}
}

// The 503 names the end of a capture that still runs. A clip that has
// ended no longer holds a slot, so its end is not in the detail.
func TestTheBusyDetailNamesACaptureThatStillRuns(t *testing.T) {
	server := newCaptureFixture(t)
	server.perOutput = 2
	if _, f := server.take("HDMI-A-1", time.Time{}); f != nil {
		t.Fatalf("the stream was refused: %v", f)
	}
	releaseClip, f := server.take("HDMI-A-1", time.Date(2026, 11, 18, 10, 5, 0, 0, time.UTC))
	if f != nil {
		t.Fatalf("the clip was refused: %v", f)
	}
	releaseClip()
	if _, f := server.take("HDMI-A-1", time.Time{}); f != nil {
		t.Fatalf("a second stream was refused after the clip ended: %v", f)
	}

	_, f = server.take("HDMI-A-1", time.Time{})
	if f == nil || !strings.Contains(f.detail, "until its client closes") {
		t.Errorf("the busy fault is %+v, want a detail that says the running streams end when their clients close", f)
	}
}

// display-api would also refuse this query.
func TestTheSidecarValidatesTheQueryItself(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png?framerate=30", "the-api-token")

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a framerate on a still responded with %d, want 400", resp.StatusCode)
	}
}

func TestTheSidecarServesOnlyTheInfoAndCaptureRoutes(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		status int
	}{
		{"a path outside the grammar", http.MethodGet, "/capture/HDMI-A-1", http.StatusNotFound},
		{"the discovery document", http.MethodGet, apiRoot, http.StatusNotFound},
		{"a method it does not accept", http.MethodPost, apiRoot + "/displays/HDMI-A-1/screen.png", http.StatusMethodNotAllowed},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newCaptureFixture(t)
			resp := askSidecar(t, server, row.method, row.target, "the-api-token")
			if resp.StatusCode != row.status {
				t.Errorf("%s %s responded with %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
		})
	}
}

func TestCapturesPerOutputComeFromTheEnvironment(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", 1},
		{"2", 2},
		{"0", 1},
		{"not a number", 1},
	}
	for _, row := range cases {
		t.Run(row.value, func(t *testing.T) {
			t.Setenv(capturePerOutput, row.value)
			if got := perOutputLimit(); got != row.want {
				t.Errorf("%q is a limit of %d, want %d", row.value, got, row.want)
			}
		})
	}
}

func TestTheConversionGraphComesFromTheKnobOrTheProbe(t *testing.T) {
	cases := []struct {
		name         string
		knob         string
		program      string
		wantSoftware bool
	}{
		{"a node told to convert on the CPU", softwareGraph, "#!/bin/sh\nexit 0\n", true},
		{"a node told to convert on the GPU", vaapiGraph, "#!/bin/sh\nexit 1\n", false},
		{"a driver that runs the graph", "", "#!/bin/sh\nexit 0\n", false},
		{"a driver with no post-processing", "", "#!/bin/sh\necho 'the requested VAProfile is not supported' >&2\nexit 251\n", true},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			path := t.TempDir() + "/ffmpeg"
			if err := os.WriteFile(path, []byte(row.program), 0o755); err != nil {
				t.Fatalf("writing the fake ffmpeg: %v", err)
			}
			held := ffmpegProgram
			ffmpegProgram = path
			defer func() { ffmpegProgram = held }()
			t.Setenv(captureConversion, row.knob)

			if got := chooseConversion(context.Background(), "/dev/dri/renderD128"); got != row.wantSoftware {
				t.Errorf("chooseConversion returned software=%t, want %t", got, row.wantSoftware)
			}
		})
	}
}

// With no render node, hwupload has no device to open.
func TestANodeWithNoRenderDeviceConvertsOnTheCPU(t *testing.T) {
	t.Setenv(captureConversion, "")
	if !chooseConversion(context.Background(), "") {
		t.Error("a node with no render device chose the GPU graph, want the software graph")
	}
}

// A dashboard counts the nodes that convert on the CPU from this
// gauge, with no need to read logs.
func TestTheConversionGaugeNamesTheGraph(t *testing.T) {
	readings := newCaptureMetrics(captureComponent, version)
	readings.converting(true)

	served := scrapeHandler(t, processHandler(readings.registry, func() bool { return true }))
	for _, line := range []string{
		`display_capture_conversion{graph="software"} 1`,
		`display_capture_conversion{graph="vaapi"} 0`,
	} {
		if !strings.Contains(served, line) {
			t.Errorf("the scrape has no %s, want it", line)
		}
	}
}

func TestTheInfoDocumentNamesTheConversion(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 8, height: 4, refresh: 60000}})
	server := newCaptureFixture(t)
	server.socketPath = weston.path
	server.software = true

	resp := askSidecar(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", "the-api-token")

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Conversion != softwareGraph {
		t.Errorf("the info document conversion is %q, want %q", info.Conversion, softwareGraph)
	}
}

func TestTheCapturePortChallengeNamesDisplayCapture(t *testing.T) {
	held := authenticateRealm
	authenticateRealm = `Bearer realm="display-capture"`
	defer func() { authenticateRealm = held }()

	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1/screen.png", "")

	if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer realm="display-capture"` {
		t.Errorf("the WWW-Authenticate header is %q, want the display-capture realm", got)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if document.Detail != noTokenDetail {
		t.Errorf("the 401 detail is %q, want %q", document.Detail, noTokenDetail)
	}
}

// A client chooses a decoder from the codecs parameter before it
// requests a clip.
func TestTheInfoDocumentNamesTheClipCodec(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 8, height: 4, refresh: 60000}})
	server := newCaptureFixture(t)
	server.socketPath = weston.path

	resp := askSidecar(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", "the-api-token")

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Codecs != "avc1.640029" {
		t.Errorf("the info document codecs are %q, want avc1.640029", info.Codecs)
	}
}
