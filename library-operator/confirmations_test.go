package main

// What these tests prove: the confirmations table against the
// shipped schema, and the read that says a copy holds every version one
// writer wrote up to a version.

import (
	"slices"
	"testing"
	"time"
)

// The actor string arrives as a dashed uuid and the site id reads
// back as packed upper-case hex, so the comparison normalises both.
func TestTheActorReadsAsTheHexOfItsSiteId(t *testing.T) {
	cases := []struct {
		name  string
		actor string
		want  string
	}{
		{name: "a dashed uuid", actor: "d98bd6b3-1f2e-4c5a-8b90-1234567890ab", want: sqliteAgentSiteID},
		{name: "an upper-case dashed uuid", actor: "D98BD6B3-1F2E-4C5A-8B90-1234567890AB", want: sqliteAgentSiteID},
		{name: "a packed uuid", actor: "d98bd6b31f2e4c5a8b901234567890ab", want: sqliteAgentSiteID},
		{name: "no actor at all", actor: "", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := actorHex(testCase.actor); got != testCase.want {
				t.Errorf("actorHex(%q) = %q, want %q", testCase.actor, got, testCase.want)
			}
		})
	}
}

// A copy holds a writer up to a version when it holds a version at
// least that high and records no gap that reaches back over it.
func TestWhatCountsAsHoldingAWritersVersions(t *testing.T) {
	other := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	cases := []struct {
		name    string
		held    int64
		gap     []int64
		version int64
		want    bool
	}{
		{name: "the version the writer named", held: 12, version: 12, want: true},
		{name: "a version past the one it named", held: 20, version: 12, want: true},
		{name: "a version short of the one it named", held: 8, version: 12},
		{name: "a writer the copy has never heard from", version: 12},
		{name: "a gap behind the version", held: 20, gap: []int64{4, 6}, version: 12},
		{name: "a gap at the version", held: 20, gap: []int64{12, 12}, version: 12},
		{name: "a gap past the version", held: 20, gap: []int64{14, 16}, version: 12, want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			if testCase.held > 0 {
				agent.holdVersion(t, other, testCase.held)
			}
			if testCase.gap != nil {
				agent.recordGap(t, other, testCase.gap[0], testCase.gap[1])
			}

			held, err := versionsHeld(t.Context(), catalog, other, testCase.version)
			if err != nil {
				t.Fatal(err)
			}
			if held != testCase.want {
				t.Errorf("versionsHeld = %v, want %v", held, testCase.want)
			}
		})
	}
}

// A run whose write named no actor is one no copy can prove, so the
// read answers no and asks the agent nothing.
func TestAVersionOfNoWriterIsNeverHeld(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)

	held, err := versionsHeld(t.Context(), catalog, "", 12)

	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Error("a write with no actor read as held")
	}
}

// An agent that will not answer is an error and never a copy that
// holds nothing, so a confirmer never confirms a run off a failed read.
func TestAFailedReadIsNotACopyThatHoldsNothing(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	agent.holdVersion(t, sqliteAgentActor, 20)
	agent.queriesLeft = 1

	if _, err := versionsHeld(t.Context(), catalog, sqliteAgentActor, 12); err == nil {
		t.Error("the read hid a refused query")
	}
}

// One row per confirming pod, written in place, so a pod that
// confirms the same run twice leaves one row and two pods leave two.
func TestAConfirmationIsOneRowPerConfirmingPod(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	at := time.Unix(1_700_000_000, 0).UTC()

	for _, confirmer := range []string{"movies-catalog-0", "movies-catalog-0", "movies-catalog-1"} {
		if err := catalog.UpsertConfirmation(ctx, "house/movies", workerScan, "scan-1", confirmer, 4, at); err != nil {
			t.Fatal(err)
		}
	}

	if got := agent.rowCount(t, "confirmations"); got != 2 {
		t.Errorf("confirmations = %d, want one row per confirming pod", got)
	}
}

// A confirmation the pod already wrote for one version is read
// back, and one it has not written is not, so a restarted confirmer writes
// no row again and a retried Job is confirmed afresh.
func TestTheConfirmerReadsBackItsOwnConfirmation(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.UpsertConfirmation(ctx, "house/movies", workerScan, "scan-1",
		"movies-catalog-0", 4, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		job       string
		confirmer string
		version   int64
		want      bool
	}{
		{name: "the run this pod confirmed", job: "scan-1", confirmer: "movies-catalog-0", version: 4, want: true},
		{name: "another pod's confirmation", job: "scan-1", confirmer: "movies-catalog-1", version: 4},
		{name: "another Job's run", job: "scan-2", confirmer: "movies-catalog-0", version: 4},
		// A retried pod of this Job writes a version of its own, and
		// the row that proves the version before it says nothing about it.
		{name: "a later version of this Job's run", job: "scan-1", confirmer: "movies-catalog-0", version: 5},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			held, err := catalog.confirmedBy(ctx, "house/movies", workerScan, testCase.job,
				testCase.confirmer, testCase.version)
			if err != nil {
				t.Fatal(err)
			}
			if held != testCase.want {
				t.Errorf("confirmedBy = %v, want %v", held, testCase.want)
			}
		})
	}
}

// The prune takes the confirmations of every older Job of the same
// worker, and leaves this Job's own and every other worker's.
func TestThePruneTakesTheOlderJobsOfOneWorker(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	for _, one := range []struct{ library, worker, job string }{
		{"house/movies", workerScan, "scan-1"},
		{"house/movies", workerScan, "scan-2"},
		{"house/movies", workerCleanup, "cleanup-1"},
		{"house/series", workerScan, "scan-1"},
	} {
		if err := catalog.UpsertConfirmation(ctx, one.library, one.worker, one.job,
			"movies-catalog-0", 4, time.Unix(10, 0)); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := catalog.pruneConfirmations(ctx, "house/movies", workerScan, "scan-2")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want the older scan of this library", removed)
	}
	if got := agent.rowCount(t, "confirmations"); got != 3 {
		t.Errorf("confirmations = %d, want the newest scan, the cleanup, and the other library", got)
	}
}

// The sweep takes every confirmation of a departing library and
// leaves every other library's.
func TestDeleteConfirmationsTakesOneLibrary(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	for _, library := range []string{"house/movies", "house/series"} {
		for _, worker := range []string{workerScan, workerCleanup} {
			if err := catalog.UpsertConfirmation(ctx, library, worker, "job-1",
				"movies-catalog-0", 4, time.Unix(10, 0)); err != nil {
				t.Fatal(err)
			}
		}
	}

	removed, err := catalog.DeleteConfirmations(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want the two confirmations of the departed library", removed)
	}
	if got := agent.rowsFor(t, "confirmations", "house/series"); got != 2 {
		t.Errorf("the surviving library holds %d confirmations, want both", got)
	}
}

// The shipped schema keys a confirmation by the library, the
// worker, the Job, and the confirming pod, and carries the run version
// that row proves as a cell beside them.
func TestTheConfirmationsTableCarriesTheVersionItProves(t *testing.T) {
	_, agent := newSQLiteCatalog(t)

	keys := []string{}
	rows, err := agent.db.Query(
		`SELECT name FROM pragma_table_info('confirmations') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		name := ""
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, name)
	}
	if want := []string{"library", "worker", "job", "confirmer"}; !slices.Equal(keys, want) {
		t.Errorf("primary key = %v, want %v", keys, want)
	}

	held := 0
	if err := agent.db.QueryRow(
		`SELECT count(*) FROM pragma_table_info('confirmations') WHERE name = 'version'`).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 1 {
		t.Error("the confirmations table carries no version column")
	}
}

// A retried pod of one Job writes a later version, and the row of
// the pod before it moves to that version rather than standing beside it.
func TestALaterVersionReplacesTheConfirmationBeforeIt(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()

	for _, version := range []int64{4, 9} {
		if err := catalog.UpsertConfirmation(ctx, "house/movies", workerScan, "scan-1",
			"movies-catalog-0", version, time.Unix(30, 0)); err != nil {
			t.Fatal(err)
		}
	}

	if got := agent.rowCount(t, "confirmations"); got != 1 {
		t.Fatalf("confirmations = %d, want the one row this pod holds", got)
	}
	for _, one := range []struct {
		version int64
		want    bool
	}{{version: 9, want: true}, {version: 4}} {
		held, err := catalog.confirmedBy(ctx, "house/movies", workerScan, "scan-1", "movies-catalog-0", one.version)
		if err != nil {
			t.Fatal(err)
		}
		if held != one.want {
			t.Errorf("confirmedBy version %d = %v, want %v", one.version, held, one.want)
		}
	}
}
