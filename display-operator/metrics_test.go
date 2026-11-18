package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestBuildInfoCarriesTheComponentAndVersion(t *testing.T) {
	readings := newMetrics("display-operator", "2026.09.10-001")
	body := scrape(t, readings)
	want := `liken_build_info{component="display-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("the scrape lacks %q", want)
	}
}

func TestTheRegistryServesTheGoRuntimeSeries(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	body := scrape(t, readings)
	if !strings.Contains(body, "go_goroutines ") {
		t.Error("the scrape has no go_goroutines series, want one")
	}
	if !strings.Contains(body, "process_start_time_seconds ") {
		t.Error("the scrape has no process_start_time_seconds series, want one")
	}
}

func TestAnEmptyMetricsAddressReturnsNoListener(t *testing.T) {
	listener, err := newMetrics("display-operator", "dev").listen("")
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}
	if listener != nil {
		t.Errorf("listen returned %v for an empty address, want no listener", listener)
	}
}

func TestListenReturnsTheErrorForAnAddressTheKernelRefuses(t *testing.T) {
	if _, err := newMetrics("display-operator", "dev").listen("127.0.0.1:-1"); err == nil {
		t.Error("listen returned no error for the address 127.0.0.1:-1, want one")
	}
}

func TestTheListenerStopsWithTheRun(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, readings)
		close(stopped)
	}()
	fetchMetrics(t, listener.Addr().String())

	cancel()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the metrics listener was still serving 20 seconds after the context ended, want it stopped")
	}
}

func TestReconciledRecordsTheDurationOfAPass(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	if err := readings.reconciled(kindDisplay, func() error { return nil }); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if count := testutil.CollectAndCount(readings.reconcileDuration); count != 1 {
		t.Errorf("the duration series has %d kinds, want 1", count)
	}
	body := scrape(t, readings)
	if !strings.Contains(body, `display_reconcile_duration_seconds_count{kind="Display"} 1`) {
		t.Errorf("the scrape does not count the pass, want one observation: %s", body)
	}
}

func TestReconciledCountsAFailedPassAndReturnsItsError(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	failure := errors.New("the card is gone")
	err := readings.reconciled(kindResourceSlice, func() error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("reconciled returned %v, want the error of the pass", err)
	}
	if got := testutil.ToFloat64(readings.reconcileErrors.WithLabelValues("ResourceSlice")); got != 1 {
		t.Errorf("display_reconcile_errors_total{kind=\"ResourceSlice\"} = %v, want 1", got)
	}
}

func TestReconciledRunsThePassWhenMetricsIsNil(t *testing.T) {
	var readings *metrics
	ran := false
	if err := readings.reconciled(kindDisplay, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("running the pass with nil metrics: %v", err)
	}
	if !ran {
		t.Error("a nil metrics skipped the pass instead of only skipping the recording")
	}
}

func TestWatchRestartedCountsEachRestart(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.watchRestarted(kindPod)
	readings.watchRestarted(kindPod)
	if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues("Pod")); got != 2 {
		t.Errorf("display_watch_restarts_total{kind=\"Pod\"} = %v, want 2", got)
	}
}

func TestWatchRestartedWithNilMetricsDoesNothing(t *testing.T) {
	var readings *metrics
	readings.watchRestarted(kindPod)
}

// scrape reads the registry over HTTP, as Prometheus does.
func scrape(t *testing.T, readings *metrics) string {
	t.Helper()
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the listener: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go serveMetrics(ctx, listener, readings)
	return fetchMetrics(t, listener.Addr().String())
}

func fetchMetrics(t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get("http://" + address + "/metrics")
	if err != nil {
		t.Fatalf("reading %s: %v", address, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(body)
}
