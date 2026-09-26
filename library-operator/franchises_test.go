package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// franchiseCheckout is a checkout of the franchises repository: one directory
// per franchise, each holding the files the test names, the way the claim
// carries it.
func franchiseCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// walkFranchisesIn walks one checkout with its art beside the yaml, which is
// the arrangement a test that drives no claim of its own wants.
func walkFranchisesIn(t *testing.T, files map[string]string) *walkResult {
	t.Helper()
	root := franchiseCheckout(t, files)
	return walkFranchises(root, root, "house/franchises")
}

// memberAt is the member row at one position, out of what the walk read.
func memberAt(t *testing.T, result *walkResult, position int) franchiseMemberRow {
	t.Helper()
	for _, member := range result.franchiseMembers {
		if member.Position == position {
			return member
		}
	}
	t.Fatalf("the walk wrote no member at position %d", position)
	return franchiseMemberRow{}
}

// The walk reads one directory into one franchises row. The id is
// franchise:name: and the slug of the directory name, because the file carries
// no provider id. The body holds the universe, the calendar, the eras, and the
// sources under the file's own names.
func TestTheWalkReadsAFranchiseDirectoryIntoItsRow(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{
		"Example Saga/franchise.yaml": wholeFranchiseFile,
		"Example Saga/poster.jpg":     "art",
		"Example Saga/AGENTS.md":      "the method",
	})

	result := walkFranchises(root, root, "house/franchises")

	if len(result.franchises) != 1 {
		t.Fatalf("the walk wrote %d rows, want the one directory", len(result.franchises))
	}
	row := result.franchises[0]
	if row.Id != "franchise:name:example-saga" || row.Path != "Example Saga" {
		t.Errorf("the row is %+v, want the id and path of the directory", row)
	}
	if row.Title != "Example Saga" || row.Slug != "example-saga" || row.Kind != libraryKindFranchises {
		t.Errorf("the row is %+v, want the file's own name", row)
	}
	if row.Art != "Example Saga/poster.jpg" {
		t.Errorf("art = %q, want the poster beside the yaml", row.Art)
	}
	body, err := json.Marshal(row.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"universe":"Prime"`, `"unit":"years"`, `"before":"BF"`,
		`"name":"The First Age"`, `"from":-5`, "example-saga-timeline"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body = %s, want %s in it", body, want)
		}
	}
	if result.titles != 1 || result.unidentified != 0 {
		t.Errorf("titles = %d and unidentified = %d, want one identified franchise",
			result.titles, result.unidentified)
	}
}

// One member row per entry of the order, counted from 1. The alias is the kind
// and the file's provider id, which the aliases table of every other library
// writes. An entry with no time is untimed, and its universes are the file's
// own list.
func TestTheWalkWritesOneMemberRowPerEntry(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{"Example Saga/franchise.yaml": wholeFranchiseFile})

	result := walkFranchises(root, root, "house/franchises")

	if len(result.franchiseMembers) != 3 {
		t.Fatalf("the walk wrote %d members, want the three entries", len(result.franchiseMembers))
	}
	cases := []struct {
		position  int
		kind      string
		alias     string
		title     string
		released  string
		year      int
		timed     int
		from      float64
		universes string
	}{
		{1, scopeMovie, "movie:tmdb:1201", "Example Saga: Part I", "1999-06-18", 1999, 1, -32, "[]"},
		{2, scopeSeries, "series:tvdb:810001", "Example Saga: The Long Watch", "2008-10", 2008, 1, -22, "[]"},
		{3, scopeMovie, "movie:tmdb:1202", "", "", 0, 0, 0, `["Prime","Mirror-7"]`},
	}
	for _, want := range cases {
		member := memberAt(t, result, want.position)
		if member.Kind != want.kind || member.Alias != want.alias || member.Title != want.title {
			t.Errorf("member %d = %+v, want %+v", want.position, member, want)
		}
		if member.Released != want.released || member.ReleaseYear != want.year {
			t.Errorf("member %d holds %q and the year %d, want %q and %d",
				want.position, member.Released, member.ReleaseYear, want.released, want.year)
		}
		if member.Timed != want.timed || member.TimeFrom != want.from {
			t.Errorf("member %d = %+v, want timed %d from %v", want.position, member, want.timed, want.from)
		}
		if member.Universes != want.universes {
			t.Errorf("member %d holds universes %s, want %s", want.position, member.Universes, want.universes)
		}
		if member.Franchise != "franchise:name:example-saga" {
			t.Errorf("member %d names the franchise %q", want.position, member.Franchise)
		}
	}
}

// A series with no seasons is one member row and no runs, which the page reads
// as the whole show. A season with no episodes is one run row with episode 0,
// and a range writes one run row per episode inside it.
func TestTheWalkWritesARunRowPerSeasonAndEpisode(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{
		"Example Saga/franchise.yaml": wholeFranchiseFile,
		"Copper Line/franchise.yaml":  "name: Copper Line\norder:\n  - series: tvdb:800001\n",
	})

	result := walkFranchises(root, root, "house/franchises")

	held := map[franchiseRunRow]bool{}
	for _, run := range result.franchiseRuns {
		held[run] = true
	}
	want := []franchiseRunRow{
		{Library: "house/franchises", Franchise: "franchise:name:example-saga", Position: 2, Season: 1, Episode: 0},
		{Library: "house/franchises", Franchise: "franchise:name:example-saga", Position: 2, Season: 3, Episode: 1},
		{Library: "house/franchises", Franchise: "franchise:name:example-saga", Position: 2, Season: 3, Episode: 3},
		{Library: "house/franchises", Franchise: "franchise:name:example-saga", Position: 2, Season: 3, Episode: 4},
		{Library: "house/franchises", Franchise: "franchise:name:example-saga", Position: 2, Season: 3, Episode: 5},
	}
	if len(held) != len(want) {
		t.Fatalf("the walk wrote %v, want the five runs of the one series with seasons", result.franchiseRuns)
	}
	for _, run := range want {
		if !held[run] {
			t.Errorf("the walk wrote %v, want %+v among them", result.franchiseRuns, run)
		}
	}
}

// A file that breaks the schema is counted unidentified and named, the way a
// folder that no .nfo file identifies is. The other files of the same checkout
// still write their rows.
func TestTheWalkSkipsAFileThatBreaksTheSchema(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{
		"Kestrel/franchise.yaml":     "name: Kestrel\norder: []\n",
		"Copper Line/franchise.yaml": "name: Copper Line\norder:\n  - series: tvdb:800001\n",
		"README.md":                  "the repository's own",
	})

	result := walkFranchises(root, root, "house/franchises")

	if len(result.franchises) != 1 || result.franchises[0].Title != "Copper Line" {
		t.Fatalf("the walk wrote %+v, want the one file that validates", result.franchises)
	}
	if result.unidentified != 1 || len(result.unidentifiedNames) != 1 {
		t.Fatalf("unidentified = %d and names = %v, want the one file it refused",
			result.unidentified, result.unidentifiedNames)
	}
	if result.unidentifiedNames[0] != "Kestrel" {
		t.Errorf("the walk named %q, want the directory it refused", result.unidentifiedNames[0])
	}
	if result.readError {
		t.Error("the walk marked the pass incomplete, want a refused file to sweep the rest")
	}
}

// A directory the checkout holds with no franchise.yaml is no franchise,
// because the repository's own files sit beside the franchise directories.
func TestTheWalkReadsOnlyADirectoryThatHoldsTheFile(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{
		"Copper Line/franchise.yaml": "name: Copper Line\norder:\n  - series: tvdb:800001\n",
		".git/config":                "the checkout's own",
		"docs/README.md":             "not a franchise",
	})

	result := walkFranchises(root, root, "house/franchises")

	if len(result.franchises) != 1 || result.unidentified != 0 {
		t.Errorf("the walk wrote %+v with %d unidentified, want the one franchise",
			result.franchises, result.unidentified)
	}
}

// A checkout the walk cannot read marks the pass incomplete, so the prune
// keeps the rows the catalog holds.
func TestTheWalkMarksThePassIncompleteForACheckoutItCannotRead(t *testing.T) {
	result := walkFranchises(filepath.Join(t.TempDir(), "nowhere"), t.TempDir(), "house/franchises")

	if !result.readError || len(result.franchises) != 0 {
		t.Errorf("the walk wrote %+v with readError %v, want no row and an incomplete pass",
			result.franchises, result.readError)
	}
}

// A franchise.yaml the scanner cannot read marks the pass incomplete. It is
// not the same as a file the schema refuses, which is counted unidentified
// and swept past.
func TestTheWalkTellsAFileItCannotReadFromOneItRefuses(t *testing.T) {
	root := franchiseCheckout(t, map[string]string{"Copper Line/franchise.yaml": "name: Copper Line\norder:\n  - series: tvdb:800001\n"})
	// A directory where the file goes, which every read of it refuses.
	if err := os.MkdirAll(filepath.Join(root, "Kestrel", franchiseFileName), 0o755); err != nil {
		t.Fatal(err)
	}

	result := walkFranchises(root, root, "house/franchises")

	if !result.readError {
		t.Error("the walk read the whole checkout, want the unreadable file to mark it incomplete")
	}
	if result.unidentified != 0 {
		t.Errorf("unidentified = %d, want a file it could not read counted as no such thing",
			result.unidentified)
	}
	if len(result.franchises) != 1 {
		t.Errorf("the walk wrote %+v, want the one file it could read", result.franchises)
	}
}

// An entry with no time is untimed at both ends. A span the file validated
// carries both ends, so the guard answers for the entry that carries none.
func TestTheSpanOfAnUntimedEntryIsZeroAtBothEnds(t *testing.T) {
	from := 3.0
	cases := []struct {
		name string
		span *franchiseTime
		want float64
	}{
		{"no time at all", nil, 0},
		{"a span with no end", &franchiseTime{From: &from}, 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			held := spanEnd(testCase.span, func(t franchiseTime) *float64 { return t.To })

			if held != testCase.want {
				t.Errorf("spanEnd = %v, want %v", held, testCase.want)
			}
		})
	}
}
