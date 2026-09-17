package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrapeHandler reads a registry through the same handler the listener
// serves, with no network socket behind it.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTheCaptureRegistryCarriesTheFiveSeriesThePlanNames(t *testing.T) {
	readings := newCaptureMetrics("2026.09.16-001")
	readings.readiness(true)
	readings.started(audioAspect)
	readings.finished(audioAspect, "flac", 96000, 2*time.Second)
	readings.failed(failureWrongTarget)

	body := scrapeHandler(t, captureRegistryHandler(readings))
	for _, want := range []string{
		`liken_build_info{component="audio-capture",version="2026.09.16-001"} 1`,
		`audio_capture_ready 1`,
		`audio_capture_bytes_total{aspect="audio",format="flac"} 96000`,
		`audio_capture_seconds_total{aspect="audio",format="flac"} 2`,
		`audio_captures_active{aspect="audio"} 0`,
		`audio_capture_failures_total{reason="wrong-target"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape carries no %q", want)
		}
	}
}

func TestTheActiveGaugeRisesWithATapAndFallsWithIt(t *testing.T) {
	readings := newCaptureMetrics("dev")
	readings.started(audioAspect)
	readings.started(audioAspect)
	if !strings.Contains(scrapeHandler(t, captureRegistryHandler(readings)),
		`audio_captures_active{aspect="audio"} 2`) {
		t.Error("two taps did not reach the gauge")
	}
	readings.finished(audioAspect, "wav", 100, time.Second)
	if !strings.Contains(scrapeHandler(t, captureRegistryHandler(readings)),
		`audio_captures_active{aspect="audio"} 1`) {
		t.Error("a tap that ended did not leave the gauge")
	}
}

func TestEveryCaptureFailureHasItsOwnName(t *testing.T) {
	readings := newCaptureMetrics("dev")
	for _, reason := range []string{
		failureConnect, failureTarget, failureWrongTarget,
		failureEncoder, failureLimit, failureCertificate,
	} {
		readings.failed(reason)
	}
	body := scrapeHandler(t, captureRegistryHandler(readings))
	for _, reason := range []string{"connect", "target", "wrong-target", "encoder", "limit", "certificate"} {
		if !strings.Contains(body, `audio_capture_failures_total{reason="`+reason+`"} 1`) {
			t.Errorf("the scrape carries no failure named %q", reason)
		}
	}
}

func TestTheAPIRegistryCarriesTheFourSeriesThePlanNames(t *testing.T) {
	readings := newAPIMetrics("2026.09.16-001")
	readings.answered("/v1/audio/sinks/{name}/audio.wav", http.MethodGet, http.StatusOK, 40*time.Millisecond)
	readings.streaming(audioAspect, 1)
	readings.certificateExpiry(time.Unix(1789000000, 0))

	body := scrapeHandler(t, apiRegistryHandler(readings, func() bool { return true }))
	for _, want := range []string{
		`liken_build_info{component="audio-api",version="2026.09.16-001"} 1`,
		`audio_api_requests_total{method="GET",route="/v1/audio/sinks/{name}/audio.wav",status="200"} 1`,
		`audio_api_request_seconds_count{route="/v1/audio/sinks/{name}/audio.wav"} 1`,
		`audio_api_streams_active{aspect="audio"} 1`,
		`audio_api_certificate_expiry_seconds 1.789e+09`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape carries no %q", want)
		}
	}
}

func TestTheRouteLabelIsNeverAConcretePath(t *testing.T) {
	harness := newAPIHarness(t)
	harness.holds("usb-0573-1573-a34004801402-usb-audio", "node-1", drillPipeWireNode)
	answer := harness.call(t, http.MethodGet,
		"/v1/audio/sinks/usb-0573-1573-a34004801402-usb-audio/audio.wav", nil)
	_, _ = io.Copy(io.Discard, answer.Body)

	body := scrapeHandler(t, apiRegistryHandler(harness.server.readings, func() bool { return true }))
	if strings.Contains(body, "usb-0573-1573-a34004801402-usb-audio") {
		t.Errorf("an endpoint's name reached Prometheus:\n%s", body)
	}
	if !strings.Contains(body, `route="/v1/audio/sinks/{name}/audio.wav"`) {
		t.Errorf("the route label is not the template:\n%s", body)
	}
}

func TestTheAPIsProbesAnswerOnTheMetricsPort(t *testing.T) {
	readings := newAPIMetrics("dev")
	ready := false
	handler := apiRegistryHandler(readings, func() bool { return ready })

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("/healthz answered %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz answered %d before a certificate", recorder.Code)
	}

	ready = true
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("/readyz answered %d with a certificate", recorder.Code)
	}
}
