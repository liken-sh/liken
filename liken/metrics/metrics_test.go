package metrics

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// scrape reads an operator's registry the way Prometheus reads it:
// one HTTP GET against the real handler, and the text document that
// comes back. Every test here asserts on that document, because the
// document is what a scraper sees.
func scrape(t *testing.T, o *Operator) string {
	t.Helper()
	server := httptest.NewServer(o.Handler())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the scrape answered %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
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

// requireSeries fails the test unless the scrape holds exactly this
// one sample line.
func requireSeries(t *testing.T, body, line string) {
	t.Helper()
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	found := series(body, name)
	if !slices.Contains(found, line) {
		t.Errorf("the scrape has no %q; it has %q", line, found)
	}
}

func TestBuildInfoNamesTheComponentAndItsRelease(t *testing.T) {
	o := NewOperator("liken-machine-operator", "2026.09.10-001", nil, nil)
	requireSeries(t, scrape(t, o),
		`liken_build_info{component="liken-machine-operator",version="2026.09.10-001"} 1`)
}

func TestTheRuntimeLayerReportsTheProcess(t *testing.T) {
	body := scrape(t, NewOperator("liken-cluster-operator", "dev", nil, nil))
	for _, name := range []string{"go_goroutines", "go_memstats_alloc_bytes", "process_start_time_seconds"} {
		t.Run(name, func(t *testing.T) {
			if len(series(body, name)) == 0 {
				t.Errorf("the scrape has no %s series", name)
			}
		})
	}
}

func TestEveryDeclaredKindPublishesAtZero(t *testing.T) {
	// The cluster operator's shape: it reconciles the `Cluster` and
	// watches `Machine` objects, so the two lists differ.
	body := scrape(t, NewOperator("liken-cluster-operator", "dev", []string{"Cluster"}, []string{"Machine"}))
	for _, line := range []string{
		`liken_reconcile_duration_seconds_count{kind="Cluster"} 0`,
		`liken_reconcile_errors_total{kind="Cluster"} 0`,
		`liken_watch_restarts_total{kind="Machine"} 0`,
	} {
		t.Run(line, func(t *testing.T) { requireSeries(t, body, line) })
	}
}

func TestAKindTheProgramDoesNotServeIsAbsent(t *testing.T) {
	// A series that is always zero is noise, so a program that runs
	// no `Machine` reconcile loop publishes no `Machine` reconcile
	// series at all.
	body := scrape(t, NewOperator("liken-cluster-operator", "dev", []string{"Cluster"}, []string{"Machine"}))
	for _, name := range []string{"liken_reconcile_errors_total", "liken_reconcile_duration_seconds_count"} {
		t.Run(name, func(t *testing.T) {
			for _, line := range series(body, name) {
				if strings.Contains(line, `kind="Machine"`) {
					t.Errorf("the scrape has %q", line)
				}
			}
		})
	}
}

func TestAPassCountsWhetherItFailsOrNot(t *testing.T) {
	o := NewOperator("liken-machine-operator", "dev", []string{"Machine"}, []string{"Machine"})
	o.ObserveReconcile("Machine", 2*time.Millisecond, nil)
	o.ObserveReconcile("Machine", 3*time.Millisecond, nil)
	o.ObserveReconcile("Machine", 5*time.Millisecond, errors.New("publishing status: the server answered 500"))

	body := scrape(t, o)
	requireSeries(t, body, `liken_reconcile_duration_seconds_count{kind="Machine"} 3`)
	requireSeries(t, body, `liken_reconcile_errors_total{kind="Machine"} 1`)
}

func TestAPassLandsInTheBucketItsDurationNames(t *testing.T) {
	o := NewOperator("liken-cluster-operator", "dev", []string{"Cluster"}, []string{"Machine"})
	o.ObserveReconcile("Cluster", 2*time.Millisecond, nil)

	body := scrape(t, o)
	requireSeries(t, body, `liken_reconcile_duration_seconds_bucket{kind="Cluster",le="0.001"} 0`)
	requireSeries(t, body, `liken_reconcile_duration_seconds_bucket{kind="Cluster",le="0.004"} 1`)
}

func TestAReopenedWatchCounts(t *testing.T) {
	o := NewOperator("liken-cluster-operator", "dev", []string{"Cluster"}, []string{"Machine"})
	o.WatchRestarted("Machine")
	o.WatchRestarted("Machine")
	requireSeries(t, scrape(t, o), `liken_watch_restarts_total{kind="Machine"} 2`)
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	o := NewOperator("liken-machine-operator", "dev", []string{"Machine"}, []string{"Machine"})
	o.ObserveReconcile("Machine", time.Millisecond, errors.New("no"))
	o.WatchRestarted("Machine")

	first := scrape(t, o)
	second := scrape(t, o)
	for _, name := range []string{
		"liken_reconcile_errors_total",
		"liken_watch_restarts_total",
		"liken_reconcile_duration_seconds_count",
		"liken_build_info",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s moved between scrapes: %q then %q", name, series(first, name), series(second, name))
			}
		})
	}
}
