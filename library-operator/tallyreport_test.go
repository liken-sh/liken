package main

// These tests prove that the rows a Job wrote reach the report the reporter
// publishes, in the shape the operator's counters read, and that the report
// holds the newest runs alone.

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// seedTallies writes the rows one container of one enrich Job left in a
// library.
func seedTallies(t *testing.T, catalog *Catalog, library, job, container string, started time.Time) {
	t.Helper()
	record := newTallies(catalog, library, workerEnrich, job, container, started)
	record.add(tallyAttempts, 4, "fact", factProbe, "result", attemptFound)
	record.add(tallyProviderRequestSeconds, 1.5, "provider", providerBlockTMDb)
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// One library's rows come back whole: the run and the container that wrote
// them, the metric, the labels as a map, and the value.
func TestTalliesReadsOneLibrarysRows(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	started := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, started)
	seedTallies(t, catalog, "house/series", "enrich-2", factProbe, started)

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	want := []libraryTally{
		{Worker: workerEnrich, Job: "enrich-1", Container: factProbe, Started: started,
			Metric: tallyAttempts,
			Labels: map[string]string{"fact": factProbe, "result": attemptFound}, Value: 4},
		{Worker: workerEnrich, Job: "enrich-1", Container: factProbe, Started: started,
			Metric: tallyProviderRequestSeconds,
			Labels: map[string]string{"provider": providerBlockTMDb}, Value: 1.5},
	}
	if len(held) != len(want) {
		t.Fatalf("tallies = %+v, want %+v", held, want)
	}
	for at, one := range want {
		if held[at].Worker != one.Worker || held[at].Job != one.Job ||
			held[at].Container != one.Container || held[at].Metric != one.Metric ||
			held[at].Value != one.Value || !held[at].Started.Equal(one.Started) {
			t.Errorf("tallies[%d] = %+v, want %+v", at, held[at], one)
		}
		for name, value := range one.Labels {
			if held[at].Labels[name] != value {
				t.Errorf("tallies[%d].Labels = %v, want %v", at, held[at].Labels, one.Labels)
			}
		}
	}
}

// Two containers of one Job each count from zero, so each has a row of its
// own and neither writes over the other.
func TestTwoContainersOfOneJobHoldTheirOwnRows(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	started := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, started)
	seedTallies(t, catalog, "house/movies", "enrich-1", nfoContainerName, started)

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	containers := map[string]float64{}
	for _, one := range held {
		if one.Metric == tallyAttempts {
			containers[one.Container] = one.Value
		}
	}
	if containers[factProbe] != 4 || containers[nfoContainerName] != 4 {
		t.Errorf("the attempts rows are %v, want 4 under each container", containers)
	}
}

// A retried pod and a Job created again both leave two runs under one Job
// name, and each run adds its own value to the counter.
func TestTwoRunsOfOneJobNameBothReachTheCounter(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	started := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, started)
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, started.Add(time.Hour))

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	if rows := tallyRowCount(t, agent, "house/movies"); rows != 4 {
		t.Errorf("the table holds %d rows, want the two rows of each run", rows)
	}
	m := newMetrics("test")
	m.observeLibraryReport(libraryNamed("movies"), &libraryReport{Tallies: held})
	got := testutil.ToFloat64(m.tallyCounters[tallyAttempts].
		WithLabelValues("movies", factProbe, attemptFound))
	if got != 8 {
		t.Errorf("attempts_total = %v, want the 4 of each run", got)
	}
}

// A library with no row has no tallies, so a report of a library whose Jobs
// have not run yet says nothing about them.
func TestTalliesAnswersNothingForALibraryWithNoRows(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Errorf("tallies = %+v, want none", held)
	}
}

// The report holds the newest runs of each worker and no more, so one
// retained message stays small while the table keeps the whole retention for
// a person reading it.
func TestTheReportCarriesTheNewestRunsOfEachWorker(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	oldest := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, oldest)
	seedTallies(t, catalog, "house/movies", "enrich-2", factProbe, oldest.Add(time.Hour))
	seedTallies(t, catalog, "house/movies", "enrich-3", factProbe, oldest.Add(2*time.Hour))

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]bool{}
	for _, one := range held {
		jobs[one.Job] = true
	}
	if jobs["enrich-1"] {
		t.Errorf("the report carries %v, want the oldest run left out", jobs)
	}
	if !jobs["enrich-2"] || !jobs["enrich-3"] {
		t.Errorf("the report carries %v, want the two newest runs", jobs)
	}
	if rows := tallyRowCount(t, agent, "house/movies"); rows != 6 {
		t.Errorf("the table holds %d rows, want the three runs it keeps for the retention", rows)
	}
}

// Each worker keeps its own newest runs, so a worker that runs often never
// pushes another worker's run out of the report.
func TestEachWorkerKeepsItsOwnNewestRuns(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	oldest := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, oldest.Add(time.Hour))
	seedTallies(t, catalog, "house/movies", "enrich-2", factProbe, oldest.Add(2*time.Hour))
	trickplay := newTallies(catalog, "house/movies", workerScan, "walk-1",
		factTrickplay, oldest)
	trickplay.add(tallyAttempts, 1, "fact", factTrickplay, "result", attemptFound)
	if err := trickplay.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	held, err := catalog.Tallies(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	workers := map[string]bool{}
	for _, one := range held {
		workers[one.Worker] = true
	}
	if !workers[workerScan] {
		t.Errorf("the report carries %v, want the scan run the enricher's runs did not push out", workers)
	}
}

// The report the reporter publishes holds the rows, because the containers
// that wrote them have exited and the bus is the one path to the operator.
func TestTheReportCarriesTheTallies(t *testing.T) {
	report, catalog := seededReporter(t)
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0).UTC())

	built, err := report.buildReport(t.Context(), "house/movies")

	if err != nil {
		t.Fatal(err)
	}
	if len(built.Tallies) != 2 {
		t.Fatalf("tallies = %+v, want the two rows the Job wrote", built.Tallies)
	}
	if built.Tallies[0].Metric != tallyAttempts || built.Tallies[0].Value != 4 {
		t.Errorf("tallies[0] = %+v, want the attempts row", built.Tallies[0])
	}
}

// A departed library's rows leave with the rest of its catalog, so the table
// holds nothing for a Library the namespace no longer has.
func TestTheWholeLibrarySweepTakesTheTallies(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	started := time.Unix(1_700_000_000, 0).UTC()
	seedTallies(t, catalog, "house/movies", "enrich-1", factProbe, started)
	seedTallies(t, catalog, "house/series", "enrich-2", factProbe, started)

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}

	if held := talliesHeld(t, agent, "house/movies"); len(held) != 0 {
		t.Errorf("the table holds %v for the departed library, want no rows", held)
	}
	if held := talliesHeld(t, agent, "house/series"); len(held) != 2 {
		t.Errorf("the table holds %v for the library that stayed, want its two rows", held)
	}
}
