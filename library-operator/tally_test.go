package main

// These tests run the recorder against the shipped schema in SQLite, so the
// upsert and the sweep are proved against the real key columns.

import (
	"sync"
	"testing"
	"time"
)

// tallyRowCount is how many rows the table holds for one library. A test
// reads it where two runs hold the same metric under the same labels.
func tallyRowCount(t *testing.T, agent *sqliteAgent, library string) int {
	t.Helper()
	count := 0
	if err := agent.db.QueryRow(
		`SELECT count(*) FROM tallies WHERE library = ?`, library).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// talliesHeld reads one library's rows as the table holds them, keyed by
// metric and labels.
func talliesHeld(t *testing.T, agent *sqliteAgent, library string) map[tallyCell]float64 {
	t.Helper()
	rows, err := agent.db.Query(
		`SELECT metric, labels, value FROM tallies WHERE library = ? ORDER BY metric, labels`, library)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	held := map[tallyCell]float64{}
	for rows.Next() {
		cell := tallyCell{}
		value := 0.0
		if err := rows.Scan(&cell.metric, &cell.labels, &value); err != nil {
			t.Fatal(err)
		}
		held[cell] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return held
}

func TestTallyLabelsSerializeInOneOrder(t *testing.T) {
	cases := []struct {
		name  string
		pairs []string
		want  string
	}{
		{name: "no labels", pairs: nil, want: ""},
		{name: "one pair", pairs: []string{"fact", "probe"}, want: "fact=probe"},
		{name: "sorted by name", pairs: []string{"result", "found", "fact", "probe"},
			want: "fact=probe,result=found"},
		{name: "a name with no value", pairs: []string{"fact"}, want: "fact="},
		{name: "the last value of a repeated name", pairs: []string{"fact", "one", "fact", "two"},
			want: "fact=two"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := tallyLabels(one.pairs); got != one.want {
				t.Errorf("tallyLabels(%v) = %q, want %q", one.pairs, got, one.want)
			}
		})
	}
}

// The same cell accumulates, and two label sets are two rows.
func TestFlushWritesTheTotalOfEveryCell(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))

	record.add(tallyAttempts, 1, "fact", "probe", "result", attemptFound)
	record.add(tallyAttempts, 1, "fact", "probe", "result", attemptFound)
	record.add(tallyAttempts, 1, "fact", "probe", "result", attemptError)
	record.add(tallyProviderRequestSeconds, 0.25, "provider", providerBlockTMDb)
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	held := talliesHeld(t, agent, "house/movies")
	want := map[tallyCell]float64{
		{metric: tallyAttempts, labels: "fact=probe,result=found"}:     2,
		{metric: tallyAttempts, labels: "fact=probe,result=error"}:     1,
		{metric: tallyProviderRequestSeconds, labels: "provider=tmdb"}: 0.25,
	}
	for cell, value := range want {
		if held[cell] != value {
			t.Errorf("tallies[%v] = %v, want %v", cell, held[cell], value)
		}
	}
	if len(held) != len(want) {
		t.Errorf("the table holds %d rows, want %d: %v", len(held), len(want), held)
	}
}

// A second flush writes the totals so far in place, so the row holds what the
// container has counted and there is never one row per flush.
func TestASecondFlushWritesTheGrownTotalInPlace(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))
	record.add(tallyProviderRequests, 3, "provider", providerBlockTMDb, "status", "2xx")
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	record.add(tallyProviderRequests, 4, "provider", providerBlockTMDb, "status", "2xx")
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	held := talliesHeld(t, agent, "house/movies")
	cell := tallyCell{metric: tallyProviderRequests, labels: "provider=tmdb,status=2xx"}
	if held[cell] != 7 {
		t.Errorf("tallies[%v] = %v, want 7", cell, held[cell])
	}
	if len(held) != 1 {
		t.Errorf("the table holds %d rows, want 1: %v", len(held), held)
	}
}

// Counts raised from several goroutines all land, because the trailer fact
// and the art fact ask their providers at once.
func TestAddCountsFromSeveralGoroutines(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))

	var raising sync.WaitGroup
	for range 50 {
		raising.Add(1)
		go func() {
			defer raising.Done()
			record.add(tallyProviderRequests, 1, "provider", providerBlockTMDb, "status", "2xx")
		}()
	}
	raising.Wait()
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	held := talliesHeld(t, agent, "house/movies")
	cell := tallyCell{metric: tallyProviderRequests, labels: "provider=tmdb,status=2xx"}
	if held[cell] != 50 {
		t.Errorf("tallies[%v] = %v, want 50", cell, held[cell])
	}
}

// A recorder with nothing in it writes nothing, so an idle container costs
// the catalog no transaction.
func TestFlushWritesNothingWithNoCounts(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))

	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	if held := talliesHeld(t, agent, "house/movies"); len(held) != 0 {
		t.Errorf("the table holds %v, want no rows", held)
	}
}

// Every method returns at once on a nil recorder, which is the state a caller
// with no catalog runs under.
func TestNilTalliesMethodsAreNoOps(t *testing.T) {
	var record *tallies

	record.add(tallyAttempts, 1, "fact", "probe")
	if err := record.flush(t.Context()); err != nil {
		t.Errorf("flush on a nil recorder = %v, want no error", err)
	}
	record.run(t.Context())
	record.recording(t.Context())()
}

// The recorder flushes once when the work that runs beside it ends.
func TestRecordingFlushesWhenTheWorkEnds(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))

	ending := record.recording(t.Context())
	record.add(tallyAttempts, 2, "fact", factProbe, "result", attemptFound)
	ending()

	held := talliesHeld(t, agent, "house/movies")
	cell := tallyCell{metric: tallyAttempts, labels: "fact=probe,result=found"}
	if held[cell] != 2 {
		t.Errorf("tallies[%v] = %v, want 2", cell, held[cell])
	}
}

// A flush the catalog refuses leaves the totals in memory, so the flush that
// follows writes them.
func TestAFailedFlushKeepsItsTotals(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	record := newTallies(catalog, "house/movies", workerEnrich, "enrich-1", factProbe,
		time.Unix(1_700_000_000, 0))
	record.add(tallyAttempts, 5, "fact", factProbe, "result", attemptFound)
	agent.transactionsLeft = 1

	if err := record.flush(t.Context()); err == nil {
		t.Fatal("the flush answered no error, want the refusal")
	}
	if err := record.flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	held := talliesHeld(t, agent, "house/movies")
	cell := tallyCell{metric: tallyAttempts, labels: "fact=probe,result=found"}
	if held[cell] != 5 {
		t.Errorf("tallies[%v] = %v, want 5", cell, held[cell])
	}
}

// The sweep deletes that worker's rows from before the cutoff and leaves the
// run in flight, the other worker's rows, and the other library's rows.
func TestSweepTalliesTakesOnlyTheWorkersOldRows(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	now := time.Unix(1_700_000_000, 0)
	old := now.Add(-tallyRetention - time.Hour)
	for _, one := range []*tallies{
		newTallies(catalog, "house/movies", workerEnrich, "enrich-old", factProbe, old),
		newTallies(catalog, "house/movies", workerEnrich, "enrich-now", factProbe, now),
		newTallies(catalog, "house/movies", workerScan, "walk-old", factTrickplay, old),
		newTallies(catalog, "house/series", workerEnrich, "enrich-old", factProbe, old),
	} {
		one.add(tallyAttempts, 1, "fact", factProbe, "result", attemptFound)
		if err := one.flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	if err := catalog.sweepTallies(t.Context(), "house/movies", workerEnrich,
		now.Add(-tallyRetention)); err != nil {
		t.Fatal(err)
	}

	jobs := []string{}
	rows, err := agent.db.Query(`SELECT job FROM tallies ORDER BY library, worker, job`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		job := ""
		if err := rows.Scan(&job); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	want := []string{"enrich-now", "walk-old", "enrich-old"}
	if len(jobs) != len(want) {
		t.Fatalf("the table holds %v, want %v", jobs, want)
	}
	for at, job := range want {
		if jobs[at] != job {
			t.Errorf("the table holds %v, want %v", jobs, want)
			break
		}
	}
}

// A sweep the catalog refuses returns the refusal, because a Job that cannot
// sweep is a Job whose catalog is not answering.
func TestSweepTalliesAnswersARefusal(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	agent.transactionsLeft = 1

	err := catalog.sweepTallies(t.Context(), "house/movies", workerEnrich, time.Now())

	if err == nil {
		t.Error("the sweep answered no error, want the refusal")
	}
}

// totals is the totals a recorder holds. A test reads it where there is no
// catalog.
func (t *tallies) totals() map[tallyCell]float64 {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	held := make(map[tallyCell]float64, len(t.held))
	for cell, value := range t.held {
		held[cell] = value
	}
	return held
}
