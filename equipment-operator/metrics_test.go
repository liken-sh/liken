package main

// This operator's metrics against a real registry, scraped through the
// real promhttp handler, the way liken/metrics proves its own layers
// one and two.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testMetrics(t *testing.T) *metrics {
	t.Helper()
	return newMetrics("dev")
}

// scrape reads a registry the way Prometheus reads it: one HTTP GET
// against the real handler, and the text document that comes back.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	server := httptest.NewServer(m.Handler())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/metrics")
	mustSucceed(t, err)
	defer resp.Body.Close()
	mustMatch(t, resp.StatusCode, http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	mustSucceed(t, err)
	return string(body)
}

// series returns every sample line in a scrape whose metric name is
// name. Comment lines are left out, so a test compares numbers and
// never help text.
func series(body, name string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			out = append(out, line)
		}
	}
	return out
}

// requireSeries fails the test unless the scrape holds exactly this one
// sample line.
func requireSeries(t *testing.T, body, line string) {
	t.Helper()
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	found := series(body, name)
	if !slices.Contains(found, line) {
		t.Errorf("the scrape has no %q; it has %q", line, found)
	}
}

// requireNoSeries fails the test unless the scrape holds no sample for
// this metric at all, which is what a fact this operator has not
// observed yet looks like.
func requireNoSeries(t *testing.T, body, name string) {
	t.Helper()
	if found := series(body, name); len(found) != 0 {
		t.Errorf("the scrape has %s, want none: %q", name, found)
	}
}

// requireValue reads the one sample whose line starts with prefix
// (name plus its label set) and fails the test unless its number
// equals want. A timestamp gauge is large enough that Prometheus
// writes it in scientific notation, so a test compares the parsed
// float and never the literal text.
func requireValue(t *testing.T, body, prefix string, want float64) {
	t.Helper()
	name, _, _ := strings.Cut(prefix, "{")
	for _, line := range series(body, name) {
		if !strings.HasPrefix(line, prefix+" ") {
			continue
		}
		_, number, _ := strings.Cut(line, " ")
		got, err := strconv.ParseFloat(number, 64)
		mustSucceed(t, err)
		mustMatch(t, got, want)
		return
	}
	t.Errorf("the scrape has no line starting %q; it has %q", prefix, series(body, name))
}

func TestBuildInfoNamesTheComponentAndItsRelease(t *testing.T) {
	requireSeries(t, scrape(t, newMetrics("2026.09.10-001")),
		`liken_build_info{component="equipment-operator",version="2026.09.10-001"} 1`)
}

func TestTheRuntimeLayerReportsTheProcess(t *testing.T) {
	body := scrape(t, testMetrics(t))
	for _, name := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_start_time_seconds"} {
		t.Run(name, func(t *testing.T) {
			if len(series(body, name)) == 0 {
				t.Errorf("the scrape has no %s series", name)
			}
		})
	}
}

// Every declared kind and outcome publishes at zero from the first
// scrape, so Prometheus has an earlier reading to compute a rate
// against the moment one first moves.
func TestTheReconcileWatchAndCommandSeriesPublishAtZero(t *testing.T) {
	body := scrape(t, testMetrics(t))
	for _, line := range []string{
		`equipment_reconcile_duration_seconds_count{kind="Receiver"} 0`,
		`equipment_reconcile_errors_total{kind="Receiver"} 0`,
		`equipment_watch_restarts_total{kind="Receiver"} 0`,
		`equipment_commands_total{status="ok"} 0`,
		`equipment_commands_total{status="failed"} 0`,
		`equipment_commands_total{status="timeout"} 0`,
	} {
		t.Run(line, func(t *testing.T) { requireSeries(t, body, line) })
	}
}

func TestAPassCountsWhetherItFailsOrNot(t *testing.T) {
	m := testMetrics(t)
	m.observeReconcile(2*time.Millisecond, nil)
	m.observeReconcile(3*time.Millisecond, nil)
	m.observeReconcile(5*time.Millisecond, errors.New("listing receivers: the server answered 500"))

	body := scrape(t, m)
	requireSeries(t, body, `equipment_reconcile_duration_seconds_count{kind="Receiver"} 3`)
	requireSeries(t, body, `equipment_reconcile_errors_total{kind="Receiver"} 1`)
}

func TestAReopenedWatchCounts(t *testing.T) {
	m := testMetrics(t)
	m.watchRestarted()
	m.watchRestarted()
	requireSeries(t, scrape(t, m), `equipment_watch_restarts_total{kind="Receiver"} 2`)
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	m := testMetrics(t)
	m.observeReconcile(time.Millisecond, errors.New("no"))
	m.watchRestarted()
	m.reportCommand(commandOK)

	first := scrape(t, m)
	second := scrape(t, m)
	for _, name := range []string{
		"equipment_reconcile_errors_total",
		"equipment_watch_restarts_total",
		"equipment_reconcile_duration_seconds_count",
		"equipment_commands_total",
		"liken_build_info",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s moved between scrapes: %q then %q", name, series(first, name), series(second, name))
			}
		})
	}
}

func TestRecordObservationOnAConnectedReceiver(t *testing.T) {
	m := testMetrics(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	m.recordObservation("theater", connectedState(), now)

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 1`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 1`)
	requireValue(t, body, `equipment_observation_last_success_timestamp_seconds{source="theater"}`, float64(now.Unix()))
	requireSeries(t, body, `equipment_receiver_power{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_receiver_volume{receiver="theater"} 50`)
	requireSeries(t, body, `equipment_receiver_input_info{input="MPLAY",receiver="theater"} 1`)
}

// A receiver the operator has never reached reports connected and
// observation_valid at zero, and leaves power, volume, and the input
// off the scrape entirely: there is no fact yet, and a fabricated zero
// would read as a receiver on standby with its volume all the way down.
func TestRecordObservationOnAReceiverThatHasNeverAnswered(t *testing.T) {
	m := testMetrics(t)

	m.recordObservation("theater", newDenonState(), time.Now())

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 0`)
	requireNoSeries(t, body, "equipment_observation_last_success_timestamp_seconds")
	requireNoSeries(t, body, "equipment_receiver_power")
	requireNoSeries(t, body, "equipment_receiver_volume")
	requireNoSeries(t, body, "equipment_receiver_input_info")
}

// A dropped connection is a fresh, valid observation that the receiver
// is gone, not a missing reading, so observation_valid falls with
// connected and the timestamp holds the last moment either was true.
func TestADroppedConnectionIsAValidObservationOfDisconnected(t *testing.T) {
	m := testMetrics(t)
	firstRead := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	poweredOn := connectedState()
	poweredOn.Power = powerOn
	m.recordObservation("theater", poweredOn, firstRead)

	// denon.go's record only flips Reachable on a dropped connection; it
	// never clears the rest of the state, so power still reads on here.
	dropped := poweredOn
	dropped.Reachable = ConditionFalse
	m.recordObservation("theater", dropped, firstRead.Add(time.Minute))

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 0`)
	// The timestamp is not observed again on a failed read, so it still
	// names the last successful one.
	requireValue(t, body, `equipment_observation_last_success_timestamp_seconds{source="theater"}`, float64(firstRead.Unix()))
	// A disconnect does not erase what the receiver last said, the same
	// way status.power holds its last value while Reachable is False.
	requireSeries(t, body, `equipment_receiver_power{receiver="theater"} 1`)
}

func TestInputInfoMovesToTheNewInputAndDropsTheOld(t *testing.T) {
	m := testMetrics(t)
	m.recordObservation("theater", connectedState(), time.Now())

	moved := connectedState()
	moved.Input = "GAME"
	m.recordObservation("theater", moved, time.Now())

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_input_info{input="GAME",receiver="theater"} 1`)
	if slices.Contains(series(body, "equipment_receiver_input_info"), `equipment_receiver_input_info{input="MPLAY",receiver="theater"} 1`) {
		t.Errorf("the old input is still on the scrape: %q", series(body, "equipment_receiver_input_info"))
	}
}

func TestSetClaimedTracksWhetherASessionStands(t *testing.T) {
	m := testMetrics(t)

	m.setClaimed("theater", true)
	requireSeries(t, scrape(t, m), `equipment_receiver_claimed{receiver="theater"} 1`)

	m.setClaimed("theater", false)
	requireSeries(t, scrape(t, m), `equipment_receiver_claimed{receiver="theater"} 0`)
}

// forgetReceiver takes a Receiver that is gone off every gauge scoped
// to one, but never off the command counter: a command already sent
// still happened.
func TestForgetReceiverRemovesTheReceiverScopedSeries(t *testing.T) {
	m := testMetrics(t)
	m.recordObservation("theater", connectedState(), time.Now())
	m.setClaimed("theater", true)
	m.reportCommand(commandOK)

	m.forgetReceiver("theater")

	body := scrape(t, m)
	for _, name := range []string{
		"equipment_receiver_connected", "equipment_receiver_claimed",
		"equipment_observation_valid", "equipment_observation_last_success_timestamp_seconds",
		"equipment_receiver_power", "equipment_receiver_volume", "equipment_receiver_input_info",
	} {
		requireNoSeries(t, body, name)
	}
	requireSeries(t, body, `equipment_commands_total{status="ok"} 1`)
}

// reportCommand on a nil *metrics is what a denonClient a test builds
// with no metrics wiring calls, and it must never panic.
func TestReportCommandOnANilMetricsIsANoOp(t *testing.T) {
	var m *metrics
	m.reportCommand(commandOK)
}

func TestTheListenerServesTheRegistry(t *testing.T) {
	m := testMetrics(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	addr, err := m.Serve(ctx, "127.0.0.1:0")
	mustSucceed(t, err)

	resp, err := http.Get("http://" + addr.String() + "/metrics")
	mustSucceed(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	mustSucceed(t, err)
	if !strings.Contains(string(body), "liken_build_info") {
		t.Errorf("the listener served no build info:\n%s", body)
	}
}

func TestAnEmptyAddressServesNoMetrics(t *testing.T) {
	addr, err := testMetrics(t).Serve(t.Context(), "")
	mustSucceed(t, err)
	if addr != nil {
		t.Errorf("an empty address opened %s", addr)
	}
}

func TestTheListenerStopsWhenItsContextEnds(t *testing.T) {
	m := testMetrics(t)
	ctx, cancel := context.WithCancel(t.Context())

	addr, err := m.Serve(ctx, "127.0.0.1:0")
	mustSucceed(t, err)
	cancel()

	deadline := time.After(testTimeout)
	for {
		if _, err := http.Get("http://" + addr.String() + "/metrics"); err != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the listener still answers after its context ended")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Send drops a command it cannot queue, and a queued command that
// reaches the socket counts ok. Both paths go through a real
// denonClient and a real fake receiver, because Send and the write
// loop are what equipment_commands_total actually observes.
func TestCommandsCountByOutcome(t *testing.T) {
	m := testMetrics(t)
	receiver := startFakeDenon(t)
	client := newDenonClient(receiver.address(), nil)
	client.readings = m

	client.Send(denonPowerOnCommand)
	requireSeries(t, scrape(t, m), `equipment_commands_total{status="failed"} 1`)

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		defer close(stopped)
		client.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	receiver.waitForCommand(t)
	deadline := time.After(testTimeout)
	for {
		if slices.Contains(series(scrape(t, m), "equipment_commands_total"), `equipment_commands_total{status="ok"} 5`) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("the five queries never counted ok: %q", series(scrape(t, m), "equipment_commands_total"))
		case <-time.After(10 * time.Millisecond):
		}
	}
}
