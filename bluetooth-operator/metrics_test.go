package main

// These tests cover the registry, liken_build_info, and the listener.
// The test of each other metric is beside the code that records it.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
)

// scrape returns the body of a GET /metrics against the handler,
// without a network listener.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	m.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Body.String()
}

// metricValue returns the value of the series with exactly these
// labels, and whether that series exists. It reads through Gather
// because a vector's WithLabelValues creates a missing series, which
// would defeat a check that the series is absent.
func metricValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !sameLabels(metric.GetLabel(), labels) {
				continue
			}
			switch {
			case metric.Gauge != nil:
				return metric.GetGauge().GetValue(), true
			case metric.Counter != nil:
				return metric.GetCounter().GetValue(), true
			case metric.Histogram != nil:
				return float64(metric.GetHistogram().GetSampleCount()), true
			}
		}
	}
	return 0, false
}

func sameLabels(got []*dto.LabelPair, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, label := range got {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}

func TestTheRuntimeCollectorsAreOnTheRegistry(t *testing.T) {
	body := scrape(t, newMetrics())
	for _, want := range []string{"go_goroutines ", "process_start_time_seconds "} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape carries no %s series", strings.TrimSpace(want))
		}
	}
}

func TestBuildInfoReportsTheComponentAndVersion(t *testing.T) {
	previous := version
	version = "2026.09.10-001"
	t.Cleanup(func() { version = previous })

	body := scrape(t, newMetrics())
	want := `liken_build_info{component="bluetooth-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("the metrics are %s, want %q in them", body, want)
	}
}

func TestAnEmptyMetricsAddressServesNoListener(t *testing.T) {
	listener, err := newMetrics().listen("")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	if listener != nil {
		t.Errorf("listen(\"\") returned the listener %v, want none", listener)
	}
}

func TestAMetricsAddressTheKernelRefusesIsReported(t *testing.T) {
	if _, err := newMetrics().listen("127.0.0.1:-1"); err == nil {
		t.Error("listen(\"127.0.0.1:-1\") returned no error, want one")
	}
}

func TestTheListenerServesTheRegistryAtSlashMetrics(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go serveMetrics(ctx, listener, m)

	answer, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("requesting /metrics: %v", err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the /metrics response: %v", err)
	}
	if answer.StatusCode != http.StatusOK {
		t.Errorf("the /metrics status is %d, want 200", answer.StatusCode)
	}
	if !strings.Contains(string(body), "liken_build_info") {
		t.Errorf("the metrics are %s, want liken_build_info in them", body)
	}
}

func TestTheMetricsListenerStopsWithTheRun(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, m)
		close(stopped)
	}()
	if _, err := http.Get("http://" + listener.Addr().String() + "/metrics"); err != nil {
		t.Fatalf("requesting /metrics: %v", err)
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the metrics listener was still serving 10 seconds after the context ended, want it stopped")
	}
}

// When the listener fails while the context is still open, serveMetrics
// logs the error and returns.
func TestAListenerThatStopsOnItsOwnIsReported(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the metrics listener: %v", err)
	}
	serveMetrics(t.Context(), listener, m)
}

// ownSeries removes the go_* and process_* lines from a scrape. Those
// collectors read the live process, so their values change between two
// scrapes with no write in between.
func ownSeries(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "go_") || strings.HasPrefix(line, "process_") ||
			strings.HasPrefix(line, "# HELP go_") || strings.HasPrefix(line, "# TYPE go_") ||
			strings.HasPrefix(line, "# HELP process_") || strings.HasPrefix(line, "# TYPE process_") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// A scrape only reads. Two scrapes with no write between them return
// the same operator series.
func TestRepeatedScrapesLeaveTheRegistryUnchanged(t *testing.T) {
	m := newMetrics()
	m.countReconcileError(peripheralKind)

	first := ownSeries(scrape(t, m))
	second := ownSeries(scrape(t, m))
	if first != second {
		t.Errorf("two scrapes of an unchanged registry differ:\n%s\n---\n%s", first, second)
	}
}
