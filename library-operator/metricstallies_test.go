package main

// These tests read the counters a Job's own counts feed, scraped through the
// registry the way a Prometheus server reads them.

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// talliedReport builds a report that holds one library's tallies and nothing
// else, so each test states only the rows it is about.
func talliedReport(held ...libraryTally) *libraryReport {
	return &libraryReport{Tallies: held}
}

// attemptsTally builds one attempts row of one container of one job.
func attemptsTally(job, fact, result string, value float64) libraryTally {
	return libraryTally{
		Worker:    workerEnrich,
		Job:       job,
		Container: fact,
		Started:   time.Unix(1_700_000_000, 0).UTC(),
		Metric:    tallyAttempts,
		Labels:    map[string]string{"fact": fact, "result": result},
		Value:     value,
	}
}

// Two containers of one Job each count from zero, so both values reach the
// counter and the later container never reads as the whole run.
func TestTwoContainersOfOneJobBothCount(t *testing.T) {
	m := newMetrics("test")
	probe := attemptsTally("enrich-1", factProbe, attemptFound, 4)
	nfo := attemptsTally("enrich-1", factProbe, attemptFound, 6)
	nfo.Container = nfoContainerName

	m.observeLibraryReport(libraryNamed("movies"), talliedReport(probe, nfo))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 10 {
		t.Errorf("attempts_total = %v, want the 4 and the 6 of both containers", got)
	}
}

// One container's growth is its own, so a report that moves one of them adds
// that container's delta alone.
func TestOneContainersGrowthIsItsOwn(t *testing.T) {
	m := newMetrics("test")
	probe := attemptsTally("enrich-1", factProbe, attemptFound, 4)
	nfo := attemptsTally("enrich-1", factProbe, attemptFound, 6)
	nfo.Container = nfoContainerName
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(probe, nfo))

	nfo.Value = 9
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(probe, nfo))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 13 {
		t.Errorf("attempts_total = %v, want the three the nfo container added", got)
	}
}

// A job the operator has not seen counted from zero inside a container that
// has exited, so its whole value is new.
func TestANewJobAddsItsWholeValue(t *testing.T) {
	m := newMetrics("test")

	m.observeLibraryReport(libraryNamed("movies"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 12)))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 12 {
		t.Errorf("attempts_total = %v, want 12", got)
	}
}

// A row that has grown adds the difference, never the whole value again,
// because the row is the run's total and the counter is the fleet's.
func TestAGrownRowAddsItsDelta(t *testing.T) {
	m := newMetrics("test")
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 12)))

	m.observeLibraryReport(libraryNamed("movies"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 30)))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 30 {
		t.Errorf("attempts_total = %v, want 30", got)
	}
}

// The reporter republishes on every change, and the backstop tick reads the
// same report again, so a row that has not moved adds nothing.
func TestAReportReadAgainAddsNothing(t *testing.T) {
	m := newMetrics("test")
	row := attemptsTally("enrich-1", factProbe, attemptFound, 12)

	m.observeLibraryReport(libraryNamed("movies"), talliedReport(row))
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(row))
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(row))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 12 {
		t.Errorf("attempts_total = %v, want 12", got)
	}
}

// A job whose rows the sweep deleted is forgotten, so a later job of the same
// name counts from zero again and adds its whole value.
func TestAVanishedJobIsForgotten(t *testing.T) {
	m := newMetrics("test")
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 12)))

	m.observeLibraryReport(libraryNamed("movies"), talliedReport())
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 5)))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 17 {
		t.Errorf("attempts_total = %v, want 17", got)
	}
}

// One library's rows are its own, so a job that vanished from one library's
// report leaves the other library's state where it is.
func TestOneLibrarysReportLeavesAnothersState(t *testing.T) {
	m := newMetrics("test")
	row := attemptsTally("enrich-1", factProbe, attemptFound, 12)
	m.observeLibraryReport(libraryNamed("movies"), talliedReport(row))
	m.observeLibraryReport(libraryNamed("series"), talliedReport(row))

	m.observeLibraryReport(libraryNamed("movies"), talliedReport())
	m.observeLibraryReport(libraryNamed("series"), talliedReport(row))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("series", factProbe, attemptFound))
	if got != 12 {
		t.Errorf("attempts_total = %v, want 12", got)
	}
}

// A Job writes a name this operator has no counter for, and the row is read
// and ignored, so a new count reaches the catalog before this table has a row
// for it.
func TestAnUnknownMetricNameIsIgnored(t *testing.T) {
	m := newMetrics("test")

	m.observeLibraryReport(libraryNamed("movies"), talliedReport(libraryTally{
		Worker: workerEnrich, Job: "enrich-1", Metric: "something_new", Value: 3,
	}))

	body := scrape(t, m)
	if strings.Contains(body, "something_new") {
		t.Errorf("scrape carries the unknown metric:\n%s", body)
	}
}

// Every metric of the table reaches its own counter under its own labels, so
// a Job that counts by site is read the same way as one that counts by fact.
func TestEveryTallyMetricReachesItsCounter(t *testing.T) {
	cases := []struct {
		name   string
		tally  libraryTally
		series string
	}{
		{name: "provider requests", series: `library_provider_requests_total{library="movies",provider="tmdb",status="429"} 4`,
			tally: libraryTally{Metric: tallyProviderRequests, Value: 4,
				Labels: map[string]string{"provider": providerBlockTMDb, "status": "429"}}},
		{name: "provider seconds", series: `library_provider_request_seconds_total{library="movies",provider="tmdb"} 2.5`,
			tally: libraryTally{Metric: tallyProviderRequestSeconds, Value: 2.5,
				Labels: map[string]string{"provider": providerBlockTMDb}}},
		{name: "trailer fetches", series: `library_trailer_fetches_total{library="movies",result="found",site="YouTube"} 9`,
			tally: libraryTally{Metric: tallyTrailerFetches, Value: 9,
				Labels: map[string]string{"site": "YouTube", "result": attemptFound}}},
		{name: "trailer bytes", series: `library_trailer_fetch_bytes_total{library="movies",site="YouTube"} 1024`,
			tally: libraryTally{Metric: tallyTrailerFetchBytes, Value: 1024,
				Labels: map[string]string{"site": "YouTube"}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			m := newMetrics("test")
			one.tally.Worker = workerEnrich
			one.tally.Job = "enrich-1"

			m.observeLibraryReport(libraryNamed("movies"), talliedReport(one.tally))

			body := scrape(t, m)
			if !strings.Contains(body, one.series) {
				t.Errorf("scrape did not contain %q:\n%s", one.series, body)
			}
		})
	}
}

// A departed Library takes its counters and its state with it, so a Library
// created later under the same name counts from zero.
func TestDropLibraryClearsItsTallies(t *testing.T) {
	m := newMetrics("test")
	m.observeLibraryReport(libraryNamed("gone"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 12)))

	m.dropLibrary("gone")
	m.observeLibraryReport(libraryNamed("gone"), talliedReport(
		attemptsTally("enrich-1", factProbe, attemptFound, 12)))

	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("gone", factProbe, attemptFound))
	if got != 12 {
		t.Errorf("attempts_total = %v, want the whole value again, 12", got)
	}
}
