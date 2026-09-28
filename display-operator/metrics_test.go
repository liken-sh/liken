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

func TestBuildInfoStatesTheComponentAndVersion(t *testing.T) {
	readings := newMetrics("display-operator", "2026.09.10-001")
	body := scrape(t, readings)
	want := `liken_build_info{component="display-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("the scrape did not carry %q", want)
	}
}

func TestTheRegistryCarriesTheGoRuntimesOwnSeries(t *testing.T) {
	// Layer 1 is the client's defaults, not only the gauge this
	// operator adds. A registry with none of them would leave every
	// process's memory and goroutine count off the fleet dashboard.
	readings := newMetrics("display-operator", "dev")
	body := scrape(t, readings)
	if !strings.Contains(body, "go_goroutines ") {
		t.Error("the scrape carries no go_goroutines series")
	}
	if !strings.Contains(body, "process_start_time_seconds ") {
		t.Error("the scrape carries no process_start_time_seconds series")
	}
}

func TestAnEmptyMetricsAddressServesNoMetrics(t *testing.T) {
	listener, err := newMetrics("display-operator", "dev").listen("")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if listener != nil {
		t.Errorf("listen answered %v, want no listener", listener)
	}
}

func TestAMetricsAddressTheKernelRefusesIsReported(t *testing.T) {
	if _, err := newMetrics("display-operator", "dev").listen("127.0.0.1:-1"); err == nil {
		t.Error("listen answered no error, want one")
	}
}

func TestTheListenerStopsWithTheRun(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		t.Fatal("the metrics listener did not stop with the run")
	}
}

func TestReconciledTimesAPassAndLeavesTheDurationSeries(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	if err := readings.reconciled(kindDisplay, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if count := testutil.CollectAndCount(readings.reconcileDuration); count != 1 {
		t.Errorf("the duration series carries %d kinds, want 1", count)
	}
	body := scrape(t, readings)
	if !strings.Contains(body, `display_reconcile_duration_seconds_count{kind="Display"} 1`) {
		t.Errorf("the scrape did not count the pass: %s", body)
	}
}

func TestReconciledCountsAnErroredPassAndReturnsIt(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	failure := errors.New("the card is gone")
	err := readings.reconciled(kindResourceSlice, func() error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("reconciled returned %v, want the pass's own error", err)
	}
	if got := testutil.ToFloat64(readings.reconcileErrors.WithLabelValues("ResourceSlice")); got != 1 {
		t.Errorf("display_reconcile_errors_total{kind=\"ResourceSlice\"} = %v, want 1", got)
	}
}

func TestReconciledRunsThePassEvenWithNoMetrics(t *testing.T) {
	var readings *metrics
	ran := false
	if err := readings.reconciled(kindDisplay, func() error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("a nil metrics skipped the pass instead of only skipping the recording")
	}
}

func TestWatchRestartedCountsEachReopening(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.watchRestarted(kindPod)
	readings.watchRestarted(kindPod)
	if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues("Pod")); got != 2 {
		t.Errorf("display_watch_restarts_total{kind=\"Pod\"} = %v, want 2", got)
	}
}

func TestWatchRestartedWithNoMetricsDoesNotPanic(t *testing.T) {
	var readings *metrics
	readings.watchRestarted(kindPod)
}

// scrape reads the registry the way a Prometheus server does: over
// HTTP, through promhttp, on a listener of the test's own.
func scrape(t *testing.T, readings *metrics) string {
	t.Helper()
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go serveMetrics(ctx, listener, readings)
	return fetchMetrics(t, listener.Addr().String())
}

func fetchMetrics(t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	answer, err := client.Get("http://" + address + "/metrics")
	if err != nil {
		t.Fatalf("reading %s: %v", address, err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(body)
}
