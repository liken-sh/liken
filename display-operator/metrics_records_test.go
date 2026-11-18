package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordingOnNilMetricsDoesNothing(t *testing.T) {
	var capture *captureMetrics
	capture.ready(true)
	capture.wrote(screenAspect, "image/png", 12)
	capture.streamed(screenAspect, "image/png", time.Second)
	capture.capturing(screenAspect, 1)
	capture.framed("image/png")
	capture.failed(deniedReason)

	var api *apiMetrics
	api.recordRequest("/v1/display", http.MethodGet, 200, time.Second)
	api.streaming(screenAspect, 1)
	api.certificateExpires(time.Now())
}

func TestScrapeReportsCaptureMetrics(t *testing.T) {
	readings := newCaptureMetrics(captureComponent, version)
	readings.ready(true)
	readings.wrote(screenAspect, "image/png", 12)
	readings.streamed(screenAspect, "image/png", 2*time.Second)
	readings.capturing(screenAspect, 1)
	readings.framed("image/png")
	readings.failed(encoderReason)

	body := scrapeHandler(t, processHandler(readings.registry, func() bool { return true }))
	for _, line := range []string{
		`display_capture_ready 1`,
		`display_capture_bytes_total{aspect="screen",format="image/png"} 12`,
		`display_capture_seconds_total{aspect="screen",format="image/png"} 2`,
		`display_captures_active{aspect="screen"} 1`,
		`display_capture_frames_total{format="image/png"} 1`,
		`display_capture_failures_total{reason="encoder"} 1`,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("the scrape has no line %s", line)
		}
	}
}

func TestScrapeLabelsAPIRequestsByRouteTemplate(t *testing.T) {
	readings := newAPIMetrics(apiComponent, version)
	readings.recordRequest("/v1/display/displays/{name}/screen.png", http.MethodGet, http.StatusOK, 250*time.Millisecond)
	readings.streaming(screenAspect, 1)
	readings.certificateExpires(time.Unix(1789000000, 0))

	body := scrapeHandler(t, processHandler(readings.registry, func() bool { return true }))
	for _, line := range []string{
		`display_api_requests_total{method="GET",route="/v1/display/displays/{name}/screen.png",status="200"} 1`,
		`display_api_request_seconds_count{route="/v1/display/displays/{name}/screen.png"} 1`,
		`display_api_streams_active{aspect="screen"} 1`,
		`display_api_certificate_expiry_seconds 1.789e+09`,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("the scrape has no line %s", line)
		}
	}
	if strings.Contains(body, "HDMI-A-1") {
		t.Error("the scrape has a label value with an output name, want only route templates")
	}
}

func TestReadyzReturns503WhileNotReady(t *testing.T) {
	handler := processHandler(newAPIMetrics(apiComponent, version).registry, func() bool { return false })
	if status := statusOf(t, handler, "/readyz"); status != http.StatusServiceUnavailable {
		t.Errorf("/readyz returned %d while not ready, want 503", status)
	}
	if status := statusOf(t, handler, "/healthz"); status != http.StatusOK {
		t.Errorf("/healthz returned %d, want 200", status)
	}
}

// scrapeHandler returns the body of GET /metrics.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Body.String()
}

func statusOf(t *testing.T, handler http.Handler, path string) int {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Code
}
