package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// gaugeValue reads what per_node_csi_copy_bytes reports for the handle.
func gaugeValue(t *testing.T, m *metrics, handle string) float64 {
	t.Helper()
	return testutil.ToFloat64(m.copyBytes.WithLabelValues(handle))
}

func TestRecordSetsTheCopyBytesOfTheVolume(t *testing.T) {
	m := newMetrics()
	m.record("example-store", 4096)
	if got := gaugeValue(t, m, "example-store"); got != 4096 {
		t.Errorf("per_node_csi_copy_bytes reads %v, want 4096", got)
	}
}

func TestForgetDeletesTheVolumesSeries(t *testing.T) {
	m := newMetrics()
	m.record("example-store", 4096)
	m.forget("example-store")
	if count := testutil.CollectAndCount(m.copyBytes); count != 0 {
		t.Errorf("the gauge has %d volumes, want none", count)
	}
}

func TestBuildInfoNamesTheComponentAndTheVersion(t *testing.T) {
	m := newMetrics()
	body := scrape(t, m)
	want := `liken_build_info{component="per-node-csi-driver",version="dev"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("the registry reads %q, want %q in it", body, want)
	}
}

func TestTheRegistryServesTheGoAndProcessMetrics(t *testing.T) {
	body := scrape(t, newMetrics())
	for _, want := range []string{"go_goroutines", "process_start_time_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("the registry reads %q, want %q in it", body, want)
		}
	}
}

func TestRecordCallCountsAnErrorUnderItsKind(t *testing.T) {
	m := newMetrics()
	m.recordCall("NodePublishVolume", time.Millisecond, nil)
	m.recordCall("NodePublishVolume", time.Millisecond, errors.New("refused"))
	if got := testutil.ToFloat64(m.reconcileErrors.WithLabelValues("NodePublishVolume")); got != 1 {
		t.Errorf("per_node_csi_reconcile_errors_total reads %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.reconcileErrors.WithLabelValues("NodeUnpublishVolume")); got != 0 {
		t.Errorf("per_node_csi_reconcile_errors_total under a kind with no call reads %v, want 0", got)
	}
}

func TestRecordCallLeavesTheDurationCountAtTheNumberOfCalls(t *testing.T) {
	m := newMetrics()
	m.recordCall("NodeGetVolumeStats", time.Millisecond, nil)
	m.recordCall("NodeGetVolumeStats", time.Millisecond, nil)
	body := scrape(t, m)
	if !strings.Contains(body, `per_node_csi_reconcile_duration_seconds_count{kind="NodeGetVolumeStats"} 2`) {
		t.Errorf("the registry reads %q, want two observations under NodeGetVolumeStats", body)
	}
}

func TestWatchRestartedCountsOneRestartUnderTheKind(t *testing.T) {
	m := newMetrics()
	m.watchRestarted(watchedKind)
	m.watchRestarted(watchedKind)
	if got := testutil.ToFloat64(m.watchRestarts.WithLabelValues(watchedKind)); got != 2 {
		t.Errorf("per_node_csi_watch_restarts_total reads %v, want 2", got)
	}
}

func TestSetVolumesReadsTheCountItWasLastSetTo(t *testing.T) {
	m := newMetrics()
	m.setVolumes(3)
	if got := testutil.ToFloat64(m.volumes); got != 3 {
		t.Errorf("per_node_csi_volumes reads %v, want 3", got)
	}
	m.setVolumes(1)
	if got := testutil.ToFloat64(m.volumes); got != 1 {
		t.Errorf("per_node_csi_volumes reads %v, want 1 after the second set", got)
	}
}

func TestMountFailedCountsOneFailure(t *testing.T) {
	m := newMetrics()
	m.mountFailed()
	if got := testutil.ToFloat64(m.mountFailures); got != 1 {
		t.Errorf("per_node_csi_mount_failures_total reads %v, want 1", got)
	}
}

// scrape returns what a Prometheus scrape of the registry reads,
// without a network listener.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorded := httptest.NewRecorder()
	m.handler().ServeHTTP(recorded, request)
	return recorded.Body.String()
}

func TestAnEmptyMetricsAddressServesNoMetrics(t *testing.T) {
	listener, err := newMetrics().listen("")
	if err != nil {
		t.Fatalf("listening on the metrics address: %v", err)
	}
	if listener != nil {
		t.Errorf("listen returned %v, want no listener", listener)
	}
}

func TestListenReturnsTheErrorForAnInvalidAddress(t *testing.T) {
	if _, err := newMetrics().listen("127.0.0.1:-1"); err == nil {
		t.Error("listen returned no error, want one")
	}
}

func TestTheListenerServesTheGaugeAtSlashMetrics(t *testing.T) {
	m := newMetrics()
	m.record("example-store", 4096)
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on the metrics address: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go serveMetrics(ctx, listener, m, quietLogger())

	body := fetch(t, "http://"+listener.Addr().String()+"/metrics")
	if !strings.Contains(body, `per_node_csi_copy_bytes{volume="example-store"} 4096`) {
		t.Errorf("the listener responded with %q, want the gauge in it", body)
	}
}

func TestServeMetricsReturnsWhenTheContextEnds(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on the metrics address: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, m, quietLogger())
		close(stopped)
	}()
	fetch(t, "http://"+listener.Addr().String()+"/metrics")

	cancel()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the metrics listener did not stop with the run")
	}
}

func TestServeMetricsReturnsWhenTheListenerIsClosed(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on the metrics address: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	serveMetrics(t.Context(), listener, m, quietLogger())
}

func fetch(t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		t.Fatalf("reading %s: %v", address, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(body)
}
