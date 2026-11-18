package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

func testMetrics(t *testing.T) *metrics {
	t.Helper()
	return newMetrics("dev")
}

// scrape calls the handler with a recorder instead of a socket, so it
// works inside a synctest bubble.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	mustMatch(t, recorder.Code, http.StatusOK)
	return recorder.Body.String()
}

// series returns the sample lines of the metric called name, without
// the HELP and TYPE lines.
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

func requireSeries(t *testing.T, body, line string) {
	t.Helper()
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	found := series(body, name)
	if !slices.Contains(found, line) {
		t.Errorf("the scrape has no line %q, and has %q", line, found)
	}
}

func requireNoSeries(t *testing.T, body, name string) {
	t.Helper()
	if found := series(body, name); len(found) != 0 {
		t.Errorf("the scrape has %s, want none: %q", name, found)
	}
}

// requireValue compares the value of the sample whose name and labels
// are prefix. A timestamp is written in scientific notation, so it
// compares parsed floats, not text.
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
	t.Errorf("the scrape has no line starting %q, and has %q", prefix, series(body, name))
}

func TestBuildInfoNamesTheComponentAndItsRelease(t *testing.T) {
	t.Parallel()
	requireSeries(t, scrape(t, newMetrics("2026.09.10-001")),
		`liken_build_info{component="equipment-operator",version="2026.09.10-001"} 1`)
}

func TestTheRuntimeLayerReportsTheProcess(t *testing.T) {
	t.Parallel()
	body := scrape(t, testMetrics(t))
	for _, name := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_start_time_seconds"} {
		t.Run(name, func(t *testing.T) {
			if len(series(body, name)) == 0 {
				t.Errorf("the scrape has no %s series", name)
			}
		})
	}
}

func TestTheReconcileWatchAndCommandSeriesPublishAtZero(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := testMetrics(t)
	m.observeReconcile(2*time.Millisecond, nil)
	m.observeReconcile(3*time.Millisecond, nil)
	m.observeReconcile(5*time.Millisecond, errors.New("listing receivers: the server answered 500"))

	body := scrape(t, m)
	requireSeries(t, body, `equipment_reconcile_duration_seconds_count{kind="Receiver"} 3`)
	requireSeries(t, body, `equipment_reconcile_errors_total{kind="Receiver"} 1`)
}

func TestAReopenedWatchCounts(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	m.watchRestarted()
	m.watchRestarted()
	requireSeries(t, scrape(t, m), `equipment_watch_restarts_total{kind="Receiver"} 2`)
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := testMetrics(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	m.recordObservation("theater", connectedState(), 2, now)

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 1`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 1`)
	requireValue(t, body, `equipment_observation_last_success_timestamp_seconds{source="theater"}`, float64(now.Unix()))
	requireSeries(t, body, `equipment_receiver_power{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_receiver_volume{receiver="theater"} 50`)
	requireSeries(t, body, `equipment_receiver_input_info{input="MPLAY",receiver="theater"} 1`)
}

// A receiver that has never replied has no power, volume, or input
// series, because a zero would read as standby with the volume at
// minimum.
func TestRecordObservationOnAReceiverThatHasNeverAnswered(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)

	m.recordObservation("theater", unreachedState(), 2, time.Now())

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 0`)
	requireNoSeries(t, body, "equipment_observation_last_success_timestamp_seconds")
	requireNoSeries(t, body, "equipment_receiver_power")
	requireNoSeries(t, body, "equipment_receiver_volume")
	requireNoSeries(t, body, "equipment_receiver_input_info")
}

func TestADroppedConnectionIsAValidObservationOfDisconnected(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	firstRead := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	poweredOn := setZone(connectedState(), func(zone *equipment.ZoneState) { zone.Power = equipment.PowerOn })

	m.recordObservation("theater", poweredOn, 2, firstRead)

	// The Denon client sets only Reachable when the connection drops.
	dropped := poweredOn
	dropped.Reachable = ConditionFalse
	m.recordObservation("theater", dropped, 2, firstRead.Add(time.Minute))

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_connected{receiver="theater"} 0`)
	requireSeries(t, body, `equipment_observation_valid{source="theater"} 0`)
	requireValue(t, body, `equipment_observation_last_success_timestamp_seconds{source="theater"}`, float64(firstRead.Unix()))
	// The power gauge keeps its last value, as status.power does while
	// Reachable is False.
	requireSeries(t, body, `equipment_receiver_power{receiver="theater"} 1`)
}

func TestInputInfoMovesToTheNewInputAndDropsTheOld(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	m.recordObservation("theater", connectedState(), 2, time.Now())

	moved := setZone(connectedState(), func(zone *equipment.ZoneState) { zone.Input = "GAME" })

	m.recordObservation("theater", moved, 2, time.Now())

	body := scrape(t, m)
	requireSeries(t, body, `equipment_receiver_input_info{input="GAME",receiver="theater"} 1`)
	if slices.Contains(series(body, "equipment_receiver_input_info"), `equipment_receiver_input_info{input="MPLAY",receiver="theater"} 1`) {
		t.Errorf("the scrape still has the previous input, want none: %q", series(body, "equipment_receiver_input_info"))
	}
}

func TestForgetReceiverRemovesTheReceiverScopedSeries(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	m.recordObservation("theater", connectedState(), 2, time.Now())
	m.reportCommand(commandOK)

	m.forgetReceiver("theater")

	body := scrape(t, m)
	for _, name := range []string{
		"equipment_receiver_connected",
		"equipment_observation_valid", "equipment_observation_last_success_timestamp_seconds",
		"equipment_receiver_power", "equipment_receiver_volume", "equipment_receiver_input_info",
	} {
		requireNoSeries(t, body, name)
	}
	requireSeries(t, body, `equipment_commands_total{status="ok"} 1`)
}

func TestReportCommandOnANilMetricsIsANoOp(t *testing.T) {
	t.Parallel()
	var m *metrics
	m.reportCommand(commandOK)
}

// listenInMemory makes Serve listen on the in-memory test network and
// returns an HTTP client that dials it. It replaces the package
// variable listen, so a test that calls it cannot run in parallel.
func listenInMemory(t *testing.T) *http.Client {
	t.Helper()
	restore := listen
	listen = testNetwork.bind
	t.Cleanup(func() { listen = restore })
	transport := &http.Transport{DialContext: testNetwork.dial, DisableKeepAlives: true}
	return &http.Client{Transport: transport}
}

func TestTheListenerServesTheRegistry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scraper := listenInMemory(t)
		m := testMetrics(t)
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		addr, err := m.Serve(ctx, "127.0.0.1:0")
		mustSucceed(t, err)

		resp, err := scraper.Get("http://" + addr.String() + "/metrics")
		mustSucceed(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		mustSucceed(t, err)
		if !strings.Contains(string(body), "liken_build_info") {
			t.Errorf("the scrape has no build info:\n%s", body)
		}
	})
}

func TestAnEmptyAddressServesNoMetrics(t *testing.T) {
	t.Parallel()
	addr, err := testMetrics(t).Serve(t.Context(), "")
	mustSucceed(t, err)
	if addr != nil {
		t.Errorf("an empty address opened a listener at %s, want none", addr)
	}
}

func TestTheListenerStopsWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scraper := listenInMemory(t)
		m := testMetrics(t)
		ctx, cancel := context.WithCancel(t.Context())

		addr, err := m.Serve(ctx, "127.0.0.1:0")
		mustSucceed(t, err)
		cancel()
		synctest.Wait()

		_, err = scraper.Get("http://" + addr.String() + "/metrics")
		mustFail(t, err)
	})
}

// A command sent before the client connects counts as failed, and each
// query that the client writes after it connects counts as ok.
func TestCommandsCountByOutcome(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := testMetrics(t)
		receiver := startFakeDenon(t)
		client := denon.NewClient(receiver.address(), nil)
		client.Dial = testNetwork.dial
		client.Reporter = m.reportCommand

		client.SetPower(equipment.MainZone, true)
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
		wanted := fmt.Sprintf(`equipment_commands_total{status="ok"} %d`, len(denon.Queries))
		deadline := time.After(testTimeout)
		for {
			if slices.Contains(series(scrape(t, m), "equipment_commands_total"), wanted) {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("equipment_commands_total never counted an ok connect query: %q", series(scrape(t, m), "equipment_commands_total"))
			case <-time.After(10 * time.Millisecond):
			}
		}
	})
}

// connectedState is a receiver that has replied to every query: on
// standby, on the MPLAY input, at a volume of 100 half-steps.
func connectedState() equipment.State {
	return equipment.State{
		Reachable: ConditionTrue,
		Zones: map[string]equipment.ZoneState{
			equipment.MainZone: {
				Power:     equipment.PowerStandby,
				Input:     "MPLAY",
				SoundMode: "MULTI CH IN",
				Volume:    100,
				VolumeMax: 139,
			},
		},
	}
}

func unreachedState() equipment.State {
	return equipment.State{
		Reachable: ConditionUnknown,
		Zones: map[string]equipment.ZoneState{
			equipment.MainZone: {Volume: equipment.Unknown, VolumeMax: equipment.Unknown},
		},
	}
}

// setZone applies change to the main zone. Zones is a map, so the
// change also reaches the state passed in.
func setZone(state equipment.State, change func(*equipment.ZoneState)) equipment.State {
	zone := state.Zones[equipment.MainZone]
	change(&zone)
	state.Zones[equipment.MainZone] = zone
	return state
}
