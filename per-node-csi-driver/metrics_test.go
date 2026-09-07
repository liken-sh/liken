package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// gaugeValue reads what per_node_copy_bytes reports for the handle.
func gaugeValue(t *testing.T, readings *metrics, handle string) float64 {
	t.Helper()
	return testutil.ToFloat64(readings.copyBytes.WithLabelValues(handle))
}

func TestTheGaugeCarriesTheCopysBytesUnderItsVolume(t *testing.T) {
	readings := newMetrics()
	readings.record("example-store", 4096)
	if got := gaugeValue(t, readings, "example-store"); got != 4096 {
		t.Errorf("per_node_copy_bytes reads %v, want 4096", got)
	}
}

func TestAVolumeTheNodeNoLongerPublishesLeavesTheGauge(t *testing.T) {
	readings := newMetrics()
	readings.record("example-store", 4096)
	readings.forget("example-store")
	if count := testutil.CollectAndCount(readings.copyBytes); count != 0 {
		t.Errorf("the gauge carries %d volumes, want none", count)
	}
}

func TestAnEmptyMetricsAddressServesNoMetrics(t *testing.T) {
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

func TestTheListenerServesTheGaugeAtSlashMetrics(t *testing.T) {
	readings := newMetrics()
	readings.record("example-store", 4096)
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go serveMetrics(ctx, listener, readings, quietLogger())

	body := fetch(t, "http://"+listener.Addr().String()+"/metrics")
	if !strings.Contains(body, `per_node_copy_bytes{volume="example-store"} 4096`) {
		t.Errorf("the listener answered %q, want the gauge in it", body)
	}
}

func TestTheListenerStopsWithTheDriversRun(t *testing.T) {
	readings := newMetrics()
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, readings, quietLogger())
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

func TestAListenerThatFailsForAnotherReasonIsLogged(t *testing.T) {
	readings := newMetrics()
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	serveMetrics(t.Context(), listener, readings, quietLogger())
}

// fetch reads one page from the metrics listener.
func fetch(t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 20 * time.Second}
	answer, err := client.Get(address)
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
