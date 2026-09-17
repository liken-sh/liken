package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The sidecar in a test holds no compositor and no encoder, because
// the door is what these drills read: the token, the grammar, the
// one-per-output rule, and the refusals.
func newCaptureFixture(t *testing.T) *captureServer {
	t.Helper()
	readings := newCaptureMetrics(captureComponent, version)
	return &captureServer{
		tokens: newTokenCache(func(token string) (*reviewedToken, *fault) {
			switch token {
			case "the-api-token":
				return &reviewedToken{Username: "system:serviceaccount:liken-system:display-api"}, nil
			case "somebody-elses-token":
				return &reviewedToken{Username: "system:serviceaccount:default:curious"}, nil
			}
			return nil, unauthenticated("the token is not valid for the audience display-capture")
		}),
		readings:   readings,
		process:    processHandler(readings.registry, func() bool { return true }),
		socketPath: t.TempDir() + "/wayland-capture",
		perOutput:  1,
		now:        time.Now,
		running:    map[string][]runningCapture{},
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

// The three process paths take no token, because a scrape and a
// probe carry none.
func TestTheProcessPathsTakeNoToken(t *testing.T) {
	server := newCaptureFixture(t)
	for _, path := range []string{"/metrics", "/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			resp := askSidecar(t, server, http.MethodGet, path, "")
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s answered %d with no token, want 200", path, resp.StatusCode)
			}
		})
	}
}

// A scrape of the sidecar finds its own six series and the
// liken_build_info gauge every liken process states.
func TestTheSidecarServesItsOwnSeries(t *testing.T) {
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
			t.Errorf("the scrape does not carry %s", series)
		}
	}
}

// The sidecar answers display-api and nobody else. A token the
// review refuses is a 401, and a token of another account, which the
// review accepts, is a 403.
func TestTheSidecarAnswersTheAPIAlone(t *testing.T) {
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
				t.Errorf("the sidecar answered %d, want %d", resp.StatusCode, row.status)
			}
			if got := resp.Header.Get("Content-Type"); got != problemMediaType {
				t.Errorf("the refusal is typed %q", got)
			}
		})
	}
}

// A HEAD takes no frame on the sidecar either, which a 200 with no
// compositor behind the socket proves.
func TestTheSidecarHeadTakesNoFrame(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodHead,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a HEAD answered %d with no compositor, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("the HEAD answered %q", got)
	}
}

// A compositor that does not answer is a 503 with Retry-After,
// carrying the socket's own words.
func TestACompositorThatDoesNotAnswer(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a dead compositor answered %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != retryAfterSeconds {
		t.Errorf("Retry-After is %q", got)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Detail, "no such file or directory") {
		t.Errorf("the detail is %q, want the socket's own words", document.Detail)
	}
}

// One capture holds an output. A second request on it is a 503 that
// says when the first one ends, and a second output is free.
func TestOneCapturePerOutput(t *testing.T) {
	server := newCaptureFixture(t)
	release, f := server.take("HDMI-A-1", time.Time{})
	if f != nil {
		t.Fatalf("the first capture was refused: %v", f)
	}

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.mp4", "the-api-token")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a second capture answered %d, want 503", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if document.Type != problemCaptureBusy {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureBusy)
	}
	if !strings.Contains(document.Detail, "until its client closes") {
		t.Errorf("the detail is %q, want it to say when the running capture ends", document.Detail)
	}

	release()
	if _, f := server.take("HDMI-A-1", time.Time{}); f != nil {
		t.Errorf("the output stayed busy after the capture ended: %v", f)
	}
	if _, f := server.take("HDMI-A-2", time.Time{}); f != nil {
		t.Errorf("a second output was refused: %v", f)
	}
}

// The sidecar is a door of its own, so it refuses a query the API
// would have refused first.
func TestTheSidecarRefusesTheQueryToo(t *testing.T) {
	server := newCaptureFixture(t)
	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png?framerate=30", "the-api-token")

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a framerate on a still answered %d, want 400", resp.StatusCode)
	}
}

// The sidecar serves the info and capture routes and nothing else,
// so a path outside them, the discovery document included, is a 404
// and a method outside GET and HEAD is a 405.
func TestTheSidecarServesOneGrammar(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		status int
	}{
		{"a path outside the grammar", http.MethodGet, "/capture/HDMI-A-1", http.StatusNotFound},
		{"the discovery document", http.MethodGet, apiRoot, http.StatusNotFound},
		{"a method it does not answer", http.MethodPost, apiRoot + "/displays/HDMI-A-1/screen.png", http.StatusMethodNotAllowed},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			server := newCaptureFixture(t)
			resp := askSidecar(t, server, row.method, row.target, "the-api-token")
			if resp.StatusCode != row.status {
				t.Errorf("%s %s answered %d, want %d", row.method, row.target, resp.StatusCode, row.status)
			}
		})
	}
}

// The knob that says how many captures one output carries is read
// from the environment, as every concurrency knob in the three
// capture APIs is, and a value that is not a count is one.
func TestThePerOutputKnob(t *testing.T) {
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
