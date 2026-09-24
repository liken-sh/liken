package main

// These tests run the marks table against a real SQLite database loaded with
// the shipped schema, the way streamrows_test.go does, and read the walk that
// lifts the marks ledger into its rows.

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// The rows of one file, one line each in ordinal order, with an open end
// read back as the database holds it.
func marksOfFile(t *testing.T, agent *sqliteAgent, library, path string) []string {
	t.Helper()
	rows, err := agent.db.Query(`SELECT kind, start_ms, end_ms, source FROM marks `+
		`WHERE library = ? AND path = ? ORDER BY ordinal`, library, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var kind, source string
		var start, end sql.NullInt64
		if err := rows.Scan(&kind, &start, &end, &source); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, kind+" "+nullText(start)+"-"+nullText(end)+" "+source)
	}
	return lines
}

func nullText(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return time.Duration(value.Int64 * int64(time.Millisecond)).String()
}

// One video file of one library, as the walk reads it.
func markedVideo(library, path string) fileRow {
	return fileRow{Library: library, Path: path, Type: fileTypeVideo, Role: fileRolePrimary, Present: true}
}

// Every column reaches the database, and an open end is a null and not a
// zero.
func TestUpsertMarksWritesEveryColumnAndTheOpenEnds(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	file := markedVideo("house/series", "Show/Season 01/Show - S01E02.mkv")

	if _, err := catalog.UpsertMarks(t.Context(), []fileRow{file}, []markRow{
		{Library: file.Library, Path: file.Path, Ordinal: 0, Kind: markKindIntro,
			End: milliseconds(107000), Source: providerBlockTheIntroDB},
		{Library: file.Library, Path: file.Path, Ordinal: 1, Kind: markKindCredits,
			Start: milliseconds(3253000), Source: providerBlockTheIntroDB},
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"intro null-1m47s theintrodb", "credits 54m13s-null theintrodb"}
	if got := marksOfFile(t, agent, file.Library, file.Path); !slices.Equal(got, want) {
		t.Errorf("marks = %v, want %v", got, want)
	}
}

// A second write holds the fresh set alone: fewer spans drop the ordinals
// beyond them, no span drops every row, and the drop stays inside the file.
func TestUpsertMarksHoldsTheFreshSetOfEachFile(t *testing.T) {
	cases := []struct {
		name  string
		fresh []markRow
		want  []string
	}{
		{
			name: "one span of three",
			fresh: []markRow{{Library: "house/movies", Path: "One/one.mkv", Kind: markKindCredits,
				Start: milliseconds(60000), Source: providerBlockIntroDB}},
			want: []string{"credits 1m0s-null introdb"},
		},
		{name: "no span at all", want: []string{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			one, two := markedVideo("house/movies", "One/one.mkv"), markedVideo("house/movies", "Two/two.mkv")
			first := []markRow{}
			for ordinal := range 3 {
				for _, file := range []fileRow{one, two} {
					first = append(first, markRow{Library: file.Library, Path: file.Path, Ordinal: ordinal,
						Kind: markKindIntro, End: milliseconds(30000), Source: providerBlockTheIntroDB})
				}
			}
			if _, err := catalog.UpsertMarks(t.Context(), []fileRow{one, two}, first); err != nil {
				t.Fatal(err)
			}

			if _, err := catalog.UpsertMarks(t.Context(), []fileRow{one}, test.fresh); err != nil {
				t.Fatal(err)
			}

			if got := marksOfFile(t, agent, one.Library, one.Path); !slices.Equal(got, test.want) {
				t.Errorf("marks = %v, want %v", got, test.want)
			}
			if got := marksOfFile(t, agent, two.Library, two.Path); len(got) != 3 {
				t.Errorf("the other file's marks = %v, want its three", got)
			}
		})
	}
}

// The prune takes the marks of a file that left the volume.
func TestPruneLibraryTakesTheMarksOfADepartedFile(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	// One title's walk, with one span on its file.
	marked := func(id, path, key string) *walkResult {
		walk := walkOfOneTitle("house/movies", id, path, key)
		walk.marks = []markRow{{Library: "house/movies", Path: walk.files[0].Path,
			Kind: markKindIntro, End: milliseconds(30000), Source: providerBlockTheIntroDB}}
		return walk
	}
	first := time.Now().Add(-time.Hour).UnixNano()
	for _, walk := range []*walkResult{
		marked("movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
		marked("movie:tmdb:2", "Two (2002)", "movie:path:two-2002"),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}

	second := time.Now().UnixNano()
	kept := marked("movie:tmdb:1", "One (2001)", "movie:path:one-2001")
	if err := flushWalk(ctx, catalog, kept, second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", second); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "marks"); got != 1 {
		t.Errorf("marks = %d, want the marked file's one", got)
	}
	if got := marksOfFile(t, agent, "house/movies", kept.files[0].Path); len(got) != 1 {
		t.Errorf("the marked file's marks = %v, want its one", got)
	}
}

// A departed library takes its marks, and the other library keeps its own.
func TestSweepLibraryTakesItsMarks(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	for _, library := range []string{"house/movies", "studio/films"} {
		file := markedVideo(library, "One (2001)/movie.mkv")
		if _, err := catalog.UpsertMarks(t.Context(), []fileRow{file}, []markRow{
			{Library: library, Path: file.Path, Kind: markKindIntro, End: milliseconds(1), Source: providerBlockTheIntroDB},
			{Library: library, Path: file.Path, Ordinal: 1, Kind: markKindCredits, Start: milliseconds(2), Source: providerBlockTheIntroDB},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowsFor(t, "marks", "house/movies"); got != 0 {
		t.Errorf("the departed library's marks = %d, want none", got)
	}
	if got := agent.rowsFor(t, "marks", "studio/films"); got != 2 {
		t.Errorf("the other library's marks = %d, want 2", got)
	}
}

// The sweep's composite keys split into the two columns the delete names.
func TestSpanKeysSplitThePathFromTheOrdinal(t *testing.T) {
	got := spanKeys([]string{"One/one.mkv" + linkKeySeparator + "3"})
	if len(got) != 1 || got[0] != (markKey{Path: "One/one.mkv", Ordinal: 3}) {
		t.Errorf("spanKeys = %v, want the path and the ordinal", got)
	}
}

// The walk reads a season folder's marks ledger into one row per span, keyed
// on the episode's file and numbered within it, so a rebuilt catalog holds
// the marks without asking a provider again.
func TestTheWalkReadsTheMarksLedgerOfASeasonFolder(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Game of Thrones (2011)", "Season 01")
	writeFile(t, filepath.Join(root, "Game of Thrones (2011)", "tvshow.nfo"),
		`<tvshow><title>Game of Thrones</title><uniqueid type="tmdb">1399</uniqueid></tvshow>`)
	for _, name := range []string{"Game of Thrones - S01E01.mkv", "Game of Thrones - S01E02.mkv"} {
		writeFile(t, filepath.Join(season, name), "video")
	}
	writeFile(t, filepath.Join(season, likenDirectory, likenLedgerName(factMarks)), `marks:
  - {path: Game of Thrones - S01E02.mkv, kind: intro, end: 107000, source: theintrodb}
  - {path: Game of Thrones - S01E01.mkv, kind: credits, start: 3631500, end: 3699500, source: introdb}
  - {path: Game of Thrones - S01E02.mkv, kind: intro, start: 7007, end: 106482, source: theintrodb}
`)

	result := readFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries},
		filepath.Join(root, "Game of Thrones (2011)"))

	got := []string{}
	for _, row := range result.marks {
		got = append(got, row.Path+" "+strconv.Itoa(row.Ordinal)+" "+row.Kind)
	}
	want := []string{
		"Game of Thrones (2011)/Season 01/Game of Thrones - S01E02.mkv 0 intro",
		"Game of Thrones (2011)/Season 01/Game of Thrones - S01E01.mkv 0 credits",
		"Game of Thrones (2011)/Season 01/Game of Thrones - S01E02.mkv 1 intro",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if result.marks[0].Start != nil || *result.marks[0].End != 107000 {
		t.Errorf("the first span = %+v, want an open start and the end as written", result.marks[0])
	}
}
