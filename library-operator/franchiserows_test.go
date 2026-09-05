package main

import (
	"context"
	"testing"
	"time"
)

// franchiseWalk is the rows one checkout writes, through the walk that reads
// it.
func franchiseWalk(t *testing.T, files map[string]string) *walkResult {
	t.Helper()
	return walkFranchisesIn(t, files)
}

// The walk's rows land in the three tables the schema declares, with the
// column names and the keys the agent holds. The member row's alias is what
// the page joins to the aliases table.
func TestTheFranchiseRowsWriteAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	result := franchiseWalk(t, map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})

	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flushWalk(ctx, catalog, result, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{"franchises": 1, "franchise_members": 3, "franchise_runs": 5}
	for table, want := range counts {
		if held := agent.rowsFor(t, table, "house/franchises"); held != want {
			t.Errorf("%s holds %d rows, want %d", table, held, want)
		}
	}
	if !agent.holdsItem(t, "franchises", "house/franchises", "franchise:name:star-wars") {
		t.Error("the franchises table holds no row for the directory the walk read")
	}
}

// A franchise that left the repository leaves the tables on the next walk,
// through the same mark-and-sweep every other kind runs, and its members and
// runs leave with it.
func TestThePruneSweepsAFranchiseThatLeftTheRepository(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	both := map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
		"Firefly/franchise.yaml":   "name: Firefly\norder:\n  - series: tvdb:78874\n",
	}

	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flushWalk(ctx, catalog, franchiseWalk(t, both), time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	epoch := time.Now().UnixNano()
	delete(both, "Star Wars/franchise.yaml")
	if err := flushWalk(ctx, catalog, franchiseWalk(t, both), epoch); err != nil {
		t.Fatal(err)
	}
	removed, err := pruneLibrary(ctx, catalog, "house/franchises", epoch)
	if err != nil {
		t.Fatal(err)
	}

	if removed != 1+3+5 {
		t.Errorf("the prune removed %d rows, want the franchise, its members, and its runs", removed)
	}
	for table, want := range map[string]int{"franchises": 1, "franchise_members": 1, "franchise_runs": 0} {
		if held := agent.rowsFor(t, table, "house/franchises"); held != want {
			t.Errorf("%s holds %d rows, want %d", table, held, want)
		}
	}
}

// A departing Library takes its franchise rows with it: the cleanup Job sweeps
// every replicated table this library holds.
func TestTheLibrarySweepTakesTheFranchiseRows(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	result := franchiseWalk(t, map[string]string{"Star Wars/franchise.yaml": wholeFranchiseFile})

	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flushWalk(ctx, catalog, result, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SweepLibrary(ctx, "house/franchises"); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"franchises", "franchise_members", "franchise_runs"} {
		if held := agent.rowsFor(t, table, "house/franchises"); held != 0 {
			t.Errorf("%s holds %d rows, want the sweep to have taken them", table, held)
		}
	}
}

// A re-walk of one franchise whose order lost an entry leaves no member row
// behind at the position that went.
func TestThePruneSweepsAMemberTheOrderLost(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	long := "name: Alien\norder:\n  - movie: tmdb:348\n  - movie: tmdb:679\n  - movie: tmdb:8077\n"
	short := "name: Alien\norder:\n  - movie: tmdb:348\n"

	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flushWalk(ctx, catalog, franchiseWalk(t,
		map[string]string{"Alien/franchise.yaml": long}), time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	epoch := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog, franchiseWalk(t,
		map[string]string{"Alien/franchise.yaml": short}), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/franchises", epoch); err != nil {
		t.Fatal(err)
	}

	if held := agent.rowsFor(t, "franchise_members", "house/franchises"); held != 1 {
		t.Errorf("franchise_members holds %d rows, want the one entry the order kept", held)
	}
}

// The released date reaches the column the schema declares at every
// precision the file may name, and the derived year lands beside it.
func TestTheMemberRowsCarryTheReleasedDate(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	result := franchiseWalk(t, map[string]string{"Alien/franchise.yaml": "name: Alien\norder:\n" +
		"  - movie: tmdb:348\n    released: 1979\n" +
		"  - movie: tmdb:679\n    released: 1986-07\n" +
		"  - movie: tmdb:8077\n    released: 1992-05-22\n" +
		"  - movie: tmdb:8078\n"})

	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if err := flushWalk(ctx, catalog, result, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		position int
		released string
		year     int
	}{
		{1, "1979", 1979},
		{2, "1986-07", 1986},
		{3, "1992-05-22", 1992},
		{4, "", 0},
	}
	for _, want := range cases {
		released, year := heldRelease(t, agent, want.position)
		if released != want.released || year != want.year {
			t.Errorf("member %d holds %q and %d, want %q and %d",
				want.position, released, year, want.released, want.year)
		}
	}
}

// heldRelease reads the released date and the year one member row holds
// off the shipped schema.
func heldRelease(t *testing.T, agent *sqliteAgent, position int) (string, int) {
	t.Helper()
	released, year := "", 0
	err := agent.db.QueryRow(
		`SELECT released, release_year FROM franchise_members WHERE library = ? AND position = ?`,
		"house/franchises", position).Scan(&released, &year)
	if err != nil {
		t.Fatal(err)
	}
	return released, year
}

// A run key the sweep read back is the franchise, the position, the season,
// and the episode joined. A key shorter than that names no run, and the
// delete it builds reaches no row rather than guessing at one.
func TestTheRunKeysReadBackWhatTheMarkWrote(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want franchiseRunKey
	}{
		{
			name: "the whole key",
			key: "franchise:name:alien" + linkKeySeparator + "2" +
				linkKeySeparator + "3" + linkKeySeparator + "4",
			want: franchiseRunKey{Franchise: "franchise:name:alien", Position: 2, Season: 3, Episode: 4},
		},
		{
			name: "a key with no season or episode",
			key:  "franchise:name:alien" + linkKeySeparator + "2",
			want: franchiseRunKey{Franchise: "franchise:name:alien", Position: 2},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			held := franchiseRunKeys([]string{testCase.key})

			if len(held) != 1 || held[0] != testCase.want {
				t.Errorf("franchiseRunKeys = %+v, want %+v", held, testCase.want)
			}
		})
	}
}
