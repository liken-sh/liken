package main

// The registry, the build_info gauge, and the listener. The facts
// each domain file derives are tested beside that file:
// inventory_test.go covers the reconcile loop's duration and errors,
// the adapter's presence, and the BlueZ observation; peripheral_test.go
// covers the per-Peripheral gauges and the disconnect counter; and
// relay_test.go covers the input event counter.

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

// scrape reads the registry's whole page, the way a Prometheus server
// would, with no real listener in front of it.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	m.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Body.String()
}

// metricValue reads one series of a metric family by its exact set of
// labels, and reports whether the family carries that series at all.
// Reading through Gather, rather than through a vector's
// WithLabelValues, matters for an absence check: WithLabelValues
// creates the series it does not find, and an absence check must not
// create the thing it is checking for.
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
		t.Fatalf("listen: %v", err)
	}
	if listener != nil {
		t.Errorf("listen answered %v, want no listener", listener)
	}
}

func TestAMetricsAddressTheKernelRefusesIsReported(t *testing.T) {
	if _, err := newMetrics().listen("127.0.0.1:-1"); err == nil {
		t.Error("listen answered no error, want one")
	}
}

func TestTheListenerServesTheRegistryAtSlashMetrics(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go serveMetrics(ctx, listener, m)

	answer, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("reading the metrics: %v", err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatal(err)
	}
	if answer.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", answer.StatusCode)
	}
	if !strings.Contains(string(body), "liken_build_info") {
		t.Errorf("the metrics are %s, want liken_build_info in them", body)
	}
}

func TestTheMetricsListenerStopsWithTheRun(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, m)
		close(stopped)
	}()
	if _, err := http.Get("http://" + listener.Addr().String() + "/metrics"); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the metrics listener did not stop with the run")
	}
}

// A listener that stops on its own, rather than through the context,
// still reaches the log and not a panic.
func TestAListenerThatStopsOnItsOwnIsReported(t *testing.T) {
	m := newMetrics()
	listener, err := m.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	serveMetrics(t.Context(), listener, m)
}

// Repeated scrapes read the same registry and change nothing in it.
// This is the whole shape of the promhttp handler, and the test
// states the contract this operator relies on rather than trusting an
// upstream library by assumption.
func TestRepeatedScrapesLeaveTheRegistryUnchanged(t *testing.T) {
	m := newMetrics()
	m.countReconcileError(peripheralKind)

	first := scrape(t, m)
	second := scrape(t, m)
	if first != second {
		t.Errorf("two scrapes of an unchanged registry differ:\n%s\n---\n%s", first, second)
	}
}
