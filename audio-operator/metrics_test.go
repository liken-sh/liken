package main

// These tests scrape a real registry through the promhttp handler, the
// way milestone 65 proves every operator's metrics: build the
// registry, read it back through the handler, and check the series it
// answers with. No test here opens a card, PipeWire, or a bus.

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

// scrape reads the registry through the same handler the listener
// serves, with no network socket behind it.
func scrape(t *testing.T, readings *metrics) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	readings.handler().ServeHTTP(recorder, request)
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Layer 1 is the one gauge every process in the organization carries,
// under its own component name and the version it was built from.
func TestBuildInfoNamesTheComponentAndVersion(t *testing.T) {
	readings := newMetrics("2026.09.10-001")
	body := scrape(t, readings)
	want := `liken_build_info{component="audio-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// Layer 2's duration is observed on every pass, whether or not it
// failed, and the error counter moves only on the ones that did.
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
		t.Errorf("a Source pass that never failed drew an error series:\n%s", body)
	}
}

// An empty METRICS_ADDRESS is how a pod under test runs: no listener,
// and no port bound.
func TestAnEmptyMetricsAddressServesNoMetrics(t *testing.T) {
	listener, err := newMetrics("test").listen("")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if listener != nil {
		t.Errorf("listen answered %v, want no listener", listener)
	}
}

// A real listener serves the same registry a direct scrape reads.
func TestTheListenerServesTheRegistryAtSlashMetrics(t *testing.T) {
	readings := newMetrics("2026.09.10-001")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `component="audio-operator"`) {
		t.Errorf("the listener answered %q, want liken_build_info in it", body)
	}
}

// The listener stops when the run's context ends, the way every other
// goroutine in this operator does.
func TestTheMetricsListenerStopsWithTheRun(t *testing.T) {
	readings := newMetrics("test")
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		t.Fatal("the metrics listener did not stop with the run")
	}
}

// Repeated scrapes read the registry in memory. Nothing here mutates a
// counter or reaches PipeWire, BlueZ, or a card, so two scrapes with no
// write between them read the same numbers.
func TestRepeatedScrapesLeaveCountersUnchanged(t *testing.T) {
	readings := newMetrics("test")
	readings.controlFailed(operationVolume)

	first := scrape(t, readings)
	second := scrape(t, readings)
	if first != second {
		t.Errorf("two scrapes with no write between them differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// The hardware triple's gauges come straight from the same Connected
// and Ready facts sinkstatus.go composes into the resource's
// conditions, and from whether a claim holds the endpoint.
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
				Claim:   &EndpointClaim{Namespace: "media", Name: "kitchen"},
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

// A monitor that comes back is the same write as one that never left:
// the gauge takes whatever the latest pass read.
func TestEndpointConnectedRecoversAfterAPassSeesItAgain(t *testing.T) {
	readings := newMetrics("test")
	control := &endpointControl{readings: readings}

	control.recordEndpoint(endpoint{facts: endpointFacts{
		Name: "hdmi-0", Endpoint: alsaEndpoint{HDMI: true, Monitor: false},
	}})
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues("hdmi-0")); got != 0 {
		t.Fatalf("connected = %v while no monitor answered, want 0", got)
	}

	control.recordEndpoint(endpoint{facts: endpointFacts{
		Name: "hdmi-0", Endpoint: alsaEndpoint{HDMI: true, Monitor: true},
		HasNode: true, Node: pwNode{Name: sinkNodeName(0, 0)},
	}})
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues("hdmi-0")); got != 1 {
		t.Errorf("connected = %v after the monitor answered again, want 1", got)
	}
}

// A missing observation is unknown, not disconnected. When PipeWire
// stops answering, the reconcile pass never reaches the per-endpoint
// gauges at all, so they keep the value the last good pass set, and
// only audio_observation_valid falls.
func TestObservationInvalidLeavesTheEndpointGaugesAtTheirLastValue(t *testing.T) {
	slice := &slicePublishFixture{}
	endpointAPI := newEndpointAPI()
	readings := newMetrics("test")
	graph := staticGraph(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}))
	operator := testReconciler(t, slice, graph, "pcmC0D0p")
	operator.readings = readings
	operator.control = newEndpointControl(testClient(t, endpointAPI.handler(t)), "liken-1",
		&preparedClaims{}, operator.graph, readings)
	operator.control.now = func() time.Time { return factsTime }
	operator.control.openCard = func(int) (*mixer, error) {
		return nil, errors.New("this test has no control device")
	}

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Fatalf("connected = %v before PipeWire stopped answering, want 1", got)
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues(sourcePipeWire)); got != 1 {
		t.Fatalf("audio_observation_valid{source=pipewire} = %v before the failure, want 1", got)
	}

	operator.graph = failingSinks
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues(sourcePipeWire)); got != 0 {
		t.Errorf("audio_observation_valid{source=pipewire} = %v after PipeWire stopped answering, want 0", got)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Errorf("connected fell to %v while the observation was only invalid, want the last value of 1", got)
	}
}

// A sweep is a confirmed absence, not an invalid observation: the
// machine no longer publishes the endpoint, so every gauge reports it
// gone.
func TestASweptEndpointReportsFalseOnEveryGauge(t *testing.T) {
	api := newEndpointAPI()
	readings := newMetrics("test")
	control := testEndpointControl(t, api, &writeRecord{})
	control.readings = readings

	err := control.reconcile(context.Background(), endpoint{facts: endpointFacts{
		Name: testAnalogName, Direction: directionSink, Machine: "liken-1",
		HasNode: true, Node: pwNode{Name: sinkNodeName(0, 0)},
		Claim: &EndpointClaim{Namespace: "media", Name: "kitchen"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(readings.endpointConnected.WithLabelValues(testAnalogName)); got != 1 {
		t.Fatalf("connected = %v before the sweep, want 1", got)
	}

	if err := control.sweep(map[string]bool{}); err != nil {
		t.Fatal(err)
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

// audio_control_failures_total counts a write to hardware by its own
// kind, so a volume write that fails and a codec switch that fails
// draw separate lines.
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
		t.Fatal("two failed writes reported nothing")
	}
	if got := testutil.ToFloat64(readings.controlFailures.WithLabelValues(operationVolume)); got != 1 {
		t.Errorf("audio_control_failures_total{operation=volume} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.controlFailures.WithLabelValues(operationCodec)); got != 1 {
		t.Errorf("audio_control_failures_total{operation=codec} = %v, want 1", got)
	}
}
