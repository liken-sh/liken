package main

// These tests read a real registry through the promhttp handler, as
// root plan 65 describes for every operator, and open no card,
// PipeWire, or bus.

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

// scrape returns the registry's text exposition through the handler
// that the listener serves, without a network socket.
func scrape(t *testing.T, readings *metrics) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	readings.handler().ServeHTTP(recorder, request)
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("reading the scrape: %v", err)
	}
	return string(body)
}

func TestTheRuntimeCollectorsAreOnTheRegistry(t *testing.T) {
	body := scrape(t, newMetrics("dev"))
	for _, want := range []string{"go_goroutines ", "process_start_time_seconds "} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape carries no %s series, want one", strings.TrimSpace(want))
		}
	}
}

func TestBuildInfoNamesTheComponentAndVersion(t *testing.T) {
	readings := newMetrics("2026.09.10-001")
	body := scrape(t, readings)
	want := `liken_build_info{component="audio-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

func TestReconciledObservesEveryPassAndCountsOnlyErrors(t *testing.T) {
	readings := newMetrics("test")
	readings.reconciled(SinkKind, 5*time.Millisecond, nil)
	readings.reconciled(SinkKind, 5*time.Millisecond, errors.New("writing the status: conflict"))
	readings.reconciled(SourceKind, 5*time.Millisecond, nil)

	body := scrape(t, readings)
	for _, want := range []string{
		`audio_reconcile_duration_seconds_count{kind="Sink"} 2`,
		`audio_reconcile_duration_seconds_count{kind="Source"} 1`,
		`audio_reconcile_errors_total{kind="Sink"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape did not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `audio_reconcile_errors_total{kind="Source"}`) {
		t.Errorf("a Source pass that never failed drew an error series, want none:\n%s", body)
	}
}

func TestAnEmptyMetricsAddressServesNoMetrics(t *testing.T) {
	listener, err := newMetrics("test").listen("")
	if err != nil {
		t.Fatalf(`listen("") returned %v, want no error`, err)
	}
	if listener != nil {
		t.Errorf(`listen("") returned %v, want no listener`, listener)
	}
}

func TestTheListenerServesTheRegistryAtSlashMetrics(t *testing.T) {
	readings := newMetrics("2026.09.10-001")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on 127.0.0.1:0: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveMetrics(ctx, listener, readings)

	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("scraping the listener: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the listener's response: %v", err)
	}
	if !strings.Contains(string(body), `component="audio-operator"`) {
		t.Errorf("the listener served %q, want liken_build_info in it", body)
	}
}

func TestTheMetricsListenerStopsWithTheRun(t *testing.T) {
	readings := newMetrics("test")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on 127.0.0.1:0: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		serveMetrics(ctx, listener, readings)
		close(stopped)
	}()

	cancel()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the metrics listener still serves 20 seconds after the run ended, want it stopped")
	}
}

// ownSeries drops the go_ and process_ series. Their collectors read
// the live process, so their values change between two scrapes even
// when nothing is written.
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

func TestRepeatedScrapesLeaveCountersUnchanged(t *testing.T) {
	readings := newMetrics("test")
	readings.controlFailed(operationVolume)

	first := ownSeries(scrape(t, readings))
	second := ownSeries(scrape(t, readings))
	if first != second {
		t.Errorf("two scrapes with no write between them differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestEndpointGaugesReflectTheFactsOnePassReads(t *testing.T) {
	cases := []struct {
		name                      string
		facts                     endpointFacts
		connected, ready, claimed float64
	}{
		{
			name: "connected without a node",
			facts: endpointFacts{
				Name:     "hdmi-0",
				Endpoint: alsaEndpoint{HDMI: true, Monitor: true},
			},
			connected: 1, ready: 0, claimed: 0,
		},
		{
			name: "disconnected",
			facts: endpointFacts{
				Name:     "hdmi-1",
				Endpoint: alsaEndpoint{HDMI: true, Monitor: false},
			},
			connected: 0, ready: 0, claimed: 0,
		},
		{
			name: "unclaimed",
			facts: endpointFacts{
				Name:    "analog",
				HasNode: true,
				Node:    pwNode{Name: sinkNodeName(0, 0)},
			},
			connected: 1, ready: 1, claimed: 0,
		},
		{
			name: "claimed",
			facts: endpointFacts{
				Name:    "analog",
				HasNode: true,
				Node:    pwNode{Name: sinkNodeName(0, 0)},
				Claims:  []EndpointClaim{{Namespace: "media", Name: "kitchen"}},
			},
			connected: 1, ready: 1, claimed: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			readings := newMetrics("test")
			control := &endpointControl{readings: readings}
			control.recordEndpoint(endpoint{facts: c.facts})

			if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(c.facts.Name)); got != c.connected {
				t.Errorf("audio_endpoint_connected = %v, want %v", got, c.connected)
			}
			if got := testutil.ToFloat64(readings.endpointReady.WithLabelValues(c.facts.Name)); got != c.ready {
				t.Errorf("audio_endpoint_ready = %v, want %v", got, c.ready)
			}
			if got := testutil.ToFloat64(readings.endpointClaimed.WithLabelValues(c.facts.Name)); got != c.claimed {
				t.Errorf("audio_endpoint_claimed = %v, want %v", got, c.claimed)
			}
		})
	}
}

func TestEndpointConnectedRecoversAfterAPassReadsItAgain(t *testing.T) {
	readings := newMetrics("test")
	control := &endpointControl{readings: readings}

	control.recordEndpoint(endpoint{facts: endpointFacts{
		Name: "hdmi-0", Endpoint: alsaEndpoint{HDMI: true, Monitor: false},
	}})
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues("hdmi-0")); got != 0 {
		t.Fatalf("connected = %v while no monitor was connected, want 0", got)
	}

	control.recordEndpoint(endpoint{facts: endpointFacts{
		Name: "hdmi-0", Endpoint: alsaEndpoint{HDMI: true, Monitor: true},
		HasNode: true, Node: pwNode{Name: sinkNodeName(0, 0)},
	}})
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues("hdmi-0")); got != 1 {
		t.Errorf("connected = %v after the monitor was connected again, want 1", got)
	}
}

// A failed graph read says nothing about whether an endpoint is
// connected, so only audio_observation_valid falls to 0.
func TestObservationInvalidLeavesTheEndpointGaugesAtTheirLastValue(t *testing.T) {
	slice := &slicePublishFixture{}
	endpointAPI := newEndpointAPI()
	readings := newMetrics("test")
	graph := staticGraph(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}))
	operator := testReconciler(t, slice, graph, "pcmC0D0p")
	operator.readings = readings
	operator.control = newEndpointControl(testClient(t, endpointAPI.handler(t)), objectCache{}, "liken-1",
		&preparedClaims{}, &graphFeed{poll: operator.graph}, readings)
	operator.control.now = func() time.Time { return factsTime }
	operator.control.openCard = func(int) (*mixer, error) {
		return nil, errors.New("this test has no control device")
	}

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconciling while PipeWire responds: %v", err)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Fatalf("connected = %v before PipeWire stopped responding, want 1", got)
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues(sourcePipeWire)); got != 1 {
		t.Fatalf("audio_observation_valid{source=pipewire} = %v before the failure, want 1", got)
	}

	operator.graph = failingGraph
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconciling after PipeWire stopped responding: %v", err)
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues(sourcePipeWire)); got != 0 {
		t.Errorf("audio_observation_valid{source=pipewire} = %v after PipeWire stopped responding, want 0", got)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Errorf("connected fell to %v while the observation was only invalid, want the last value of 1", got)
	}
}

// A sweep removes an endpoint that this machine no longer publishes, so
// each of its gauges reads 0.
func TestASweptEndpointReportsFalseOnEveryGauge(t *testing.T) {
	api := newEndpointAPI()
	readings := newMetrics("test")
	control := testEndpointControl(t, api, &writeRecord{})
	control.readings = readings

	err := control.reconcile(context.Background(), endpoint{facts: endpointFacts{
		Name: testAnalogName, Direction: directionSink, Machine: "liken-1",
		HasNode: true, Node: pwNode{Name: sinkNodeName(0, 0)},
		Claims: []EndpointClaim{{Namespace: "media", Name: "kitchen"}},
	}})
	if err != nil {
		t.Fatalf("reconciling the Sink before the sweep: %v", err)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Fatalf("connected = %v before the sweep, want 1", got)
	}

	if err := control.sweep(map[string]bool{}); err != nil {
		t.Fatalf("sweeping the endpoints: %v", err)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 0 {
		t.Errorf("a swept endpoint reads connected %v, want 0", got)
	}
	if got := testutil.ToFloat64(readings.endpointReady.WithLabelValues(testAnalogName)); got != 0 {
		t.Errorf("a swept endpoint reads ready %v, want 0", got)
	}
	if got := testutil.ToFloat64(readings.endpointClaimed.WithLabelValues(testAnalogName)); got != 0 {
		t.Errorf("a swept endpoint reads claimed %v, want 0", got)
	}
}

func TestControlFailuresCountByOperation(t *testing.T) {
	readings := newMetrics("test")
	control := recordingControl(&writeRecord{})
	control.readings = readings
	control.setRoute = func(context.Context, int, pwRoute, levelWrite) error {
		return errors.New("org.bluez.MediaTransport1: no reply")
	}
	control.switchCodec = func(context.Context, string, string, bluezSink) (bluezSink, error) {
		return bluezSink{}, errors.New("org.bluez.MediaTransport1.SelectCodec: no reply")
	}

	err := control.apply(context.Background(), endpoint{facts: speakerSink(50, "sbc")},
		endpointWrites{Level: &levelWrite{Volume: pointerTo(25)}, Codec: "aac"})
	if err == nil {
		t.Fatal("apply returned nil after two writes that failed, want an error")
	}
	if got := testutil.ToFloat64(readings.controlFailures.WithLabelValues(operationVolume)); got != 1 {
		t.Errorf("audio_control_failures_total{operation=volume} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.controlFailures.WithLabelValues(operationCodec)); got != 1 {
		t.Errorf("audio_control_failures_total{operation=codec} = %v, want 1", got)
	}
}
