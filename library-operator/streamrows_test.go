package main

// These tests run the streams table against a real SQLite database loaded
// with the shipped schema, the way sqlitecatalog_test.go does.

import (
	"slices"
	"testing"
	"time"
)

// One stream row with every column filled.
func fullStream(library, path string, ordinal int) streamRow {
	return streamRow{
		Library: library, Path: path, Ordinal: ordinal,
		Kind: "video", Codec: "hevc", Profile: "Main 10",
		Width: 3840, Height: 2160, Depth: 10, FrameRate: "24000/1001",
		ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc",
		DolbyVision: true, Channels: 8, Layout: "7.1", SampleRate: 48000,
		Bitrate: 64000000, Language: "eng", Title: "Main",
		Default: true, Forced: true, Present: true,
	}
}

// How many streams one file holds.
func streamsOfFile(t *testing.T, agent *sqliteAgent, library, path string) int {
	t.Helper()
	count := 0
	if err := agent.db.QueryRow(`SELECT count(*) FROM streams WHERE library = ? AND path = ?`,
		library, path).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// The columns of one stream row, as the database holds them.
func streamColumns(t *testing.T, agent *sqliteAgent, library, path string, ordinal int) []any {
	t.Helper()
	row := agent.db.QueryRow(
		`SELECT kind, codec, profile, width, height, depth, frame_rate, `+
			`color_primaries, color_transfer, color_space, dolby_vision, channels, `+
			`layout, sample_rate, bitrate, language, title, is_default, forced, present `+
			`FROM streams WHERE library = ? AND path = ? AND ordinal = ?`,
		library, path, ordinal)
	cells := make([]any, 20)
	pointers := make([]any, 20)
	for i := range cells {
		pointers[i] = &cells[i]
	}
	if err := row.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	return cells
}

// Every column reaches the database, and a second write of the same ordinal
// updates that row in place.
func TestUpsertStreamsWritesEveryColumn(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()

	if _, err := catalog.UpsertStreams(ctx, []streamRow{fullStream("house/movies", "One/one.mkv", 0)}); err != nil {
		t.Fatal(err)
	}

	want := []any{"video", "hevc", "Main 10", int64(3840), int64(2160), int64(10), "24000/1001",
		"bt2020", "smpte2084", "bt2020nc", int64(1), int64(8), "7.1", int64(48000),
		int64(64000000), "eng", "Main", int64(1), int64(1), int64(1)}
	got := streamColumns(t, agent, "house/movies", "One/one.mkv", 0)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %v, want %v", i, got[i], want[i])
		}
	}

	moved := fullStream("house/movies", "One/one.mkv", 0)
	moved.Codec = "av1"
	moved.Forced = false
	if _, err := catalog.UpsertStreams(ctx, []streamRow{moved}); err != nil {
		t.Fatal(err)
	}
	if got := agent.rowCount(t, "streams"); got != 1 {
		t.Fatalf("streams = %d, want the one row written twice", got)
	}
	again := streamColumns(t, agent, "house/movies", "One/one.mkv", 0)
	if again[1] != "av1" || again[18] != int64(0) {
		t.Errorf("codec, forced = %v, %v, want av1, 0", again[1], again[18])
	}
}

// A re-probe leaves only the streams the fresh probe read.
func TestUpsertStreamsDropsTheOrdinalsBeyondTheFreshCount(t *testing.T) {
	cases := []struct {
		name   string
		first  int
		second int
	}{
		{name: "fewer streams than before", first: 5, second: 2},
		{name: "the same count", first: 3, second: 3},
		{name: "more streams than before", first: 1, second: 4},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			ctx := t.Context()

			for _, count := range []int{one.first, one.second} {
				rows := make([]streamRow, count)
				for i := range rows {
					rows[i] = fullStream("house/movies", "One/one.mkv", i)
				}
				if _, err := catalog.UpsertStreams(ctx, rows); err != nil {
					t.Fatal(err)
				}
			}

			if got := agent.rowCount(t, "streams"); got != one.second {
				t.Errorf("streams = %d, want the %d the fresh probe read", got, one.second)
			}
		})
	}
}

// The drop of stale ordinals reaches one file's rows alone.
func TestUpsertStreamsScopesTheDropToOneFile(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()

	seed := []streamRow{
		fullStream("house/movies", "One/one.mkv", 0),
		fullStream("house/movies", "One/one.mkv", 1),
		fullStream("house/movies", "Two/two.mkv", 0),
		fullStream("house/movies", "Two/two.mkv", 1),
	}
	if _, err := catalog.UpsertStreams(ctx, seed); err != nil {
		t.Fatal(err)
	}

	if _, err := catalog.UpsertStreams(ctx, []streamRow{fullStream("house/movies", "One/one.mkv", 0)}); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "streams"); got != 3 {
		t.Errorf("streams = %d, want the one fresh stream and the other file's two", got)
	}
	if got := agent.rowsFor(t, "streams", "house/movies"); got != 3 {
		t.Errorf("streams of the library = %d, want 3", got)
	}
}

// An upsert of no streams posts no statement.
func TestUpsertStreamsWritesNothingForNoStreams(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	if _, err := catalog.UpsertStreams(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := len(recorder.all()); got != 0 {
		t.Errorf("statements = %d, want none", got)
	}
}

// Writes one file row and that file's streams.
func seedFileWithStreams(t *testing.T, catalog *Catalog, library, path string, count int) {
	t.Helper()
	ctx := t.Context()
	if _, err := catalog.UpsertFiles(ctx, []fileRow{{Library: library, Path: path, Present: true}}); err != nil {
		t.Fatal(err)
	}
	rows := make([]streamRow, count)
	for i := range rows {
		rows[i] = fullStream(library, path, i)
	}
	if _, err := catalog.UpsertStreams(ctx, rows); err != nil {
		t.Fatal(err)
	}
}

// The prune takes the streams of a file that left the volume.
func TestPruneLibraryTakesTheStreamsOfADepartedFile(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := time.Now().Add(-time.Hour).UnixNano()
	for _, title := range []struct{ id, path, key string }{
		{"movie:tmdb:1", "One (2001)", "movie:path:one-2001"},
		{"movie:tmdb:2", "Two (2002)", "movie:path:two-2002"},
	} {
		if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", title.id, title.path, title.key), first); err != nil {
			t.Fatal(err)
		}
		seedFileWithStreams(t, catalog, "house/movies", title.path+"/movie.mkv", 3)
	}
	if got := agent.rowCount(t, "streams"); got != 6 {
		t.Fatalf("streams = %d, want the three of each file", got)
	}

	second := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"), second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", second); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "streams"); got != 3 {
		t.Errorf("streams = %d, want the marked file's three", got)
	}
	if got := streamsOfFile(t, agent, "house/movies", "One (2001)/movie.mkv"); got != 3 {
		t.Errorf("the marked file's streams = %d, want 3", got)
	}
}

// A folder rescan takes the streams of the files it no longer read.
func TestPruneScopeTakesTheStreamsOfADepartedFile(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := time.Now().Add(-time.Hour).UnixNano()
	for _, title := range []struct{ id, path, key string }{
		{"movie:tmdb:1", "One (2001)", "movie:path:one-2001"},
		{"movie:tmdb:2", "Two (2002)", "movie:path:two-2002"},
	} {
		if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", title.id, title.path, title.key), first); err != nil {
			t.Fatal(err)
		}
		seedFileWithStreams(t, catalog, "house/movies", title.path+"/movie.mkv", 2)
	}

	if _, err := pruneScope(ctx, catalog, "house/movies", "One (2001)", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowCount(t, "streams"); got != 2 {
		t.Errorf("streams = %d, want the streams outside the folder", got)
	}
	if got := streamsOfFile(t, agent, "house/movies", "Two (2002)/movie.mkv"); got != 2 {
		t.Errorf("the streams outside the folder = %d, want 2", got)
	}
}

// A departed library takes its streams with its files.
func TestSweepLibraryTakesItsStreams(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()

	for _, library := range []string{"house/movies", "studio/films"} {
		seedFileWithStreams(t, catalog, library, "One (2001)/movie.mkv", 4)
	}

	if _, err := catalog.SweepLibrary(ctx, "house/movies"); err != nil {
		t.Fatal(err)
	}

	if got := agent.rowsFor(t, "streams", "house/movies"); got != 0 {
		t.Errorf("the departed library's streams = %d, want none", got)
	}
	if got := agent.rowsFor(t, "streams", "studio/films"); got != 4 {
		t.Errorf("the other library's streams = %d, want 4", got)
	}
	if got := agent.rowsFor(t, "files", "house/movies"); got != 0 {
		t.Errorf("the departed library's files = %d, want none", got)
	}
}

// The sweep's composite keys split into the two columns the delete names.
func TestStreamKeysSplitsThePathFromTheOrdinal(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want streamKey
	}{
		{name: "a plain path", key: "One/one.mkv" + linkKeySeparator + "3",
			want: streamKey{Path: "One/one.mkv", Ordinal: 3}},
		{name: "no ordinal", key: "One/one.mkv",
			want: streamKey{Path: "One/one.mkv", Ordinal: 0}},
		{name: "an ordinal that is not a number", key: "One/one.mkv" + linkKeySeparator + "x",
			want: streamKey{Path: "One/one.mkv", Ordinal: 0}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := streamKeys([]string{one.key})
			if len(got) != 1 || got[0] != one.want {
				t.Errorf("streamKeys = %v, want %v", got, one.want)
			}
		})
	}
}

// The shipped schema keys a stream on the library, the file's path, and the
// ordinal.
func TestTheSchemaKeysStreamsOnTheLibraryPathAndOrdinal(t *testing.T) {
	_, agent := newSQLiteCatalog(t)

	rows, err := agent.db.Query(`SELECT name FROM pragma_table_info('streams') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var key []string
	for rows.Next() {
		name := ""
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		key = append(key, name)
	}
	if want := []string{"library", "path", "ordinal"}; !slices.Equal(key, want) {
		t.Errorf("primary key = %v, want %v", key, want)
	}
}

// The files table holds the container's bit rate and the probe mark, and
// the upsert writes both.
func TestTheFilesTableHoldsTheBitrateAndTheProbeMark(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)

	row := fileRow{Library: "house/movies", Path: "One/one.mkv", Present: true,
		Bitrate: 64000000, Probed: 1700000000}
	if _, err := catalog.UpsertFiles(t.Context(), []fileRow{row}); err != nil {
		t.Fatal(err)
	}

	var bitrate, probed int64
	if err := agent.db.QueryRow(`SELECT bitrate, probed FROM files WHERE library = ? AND path = ?`,
		row.Library, row.Path).Scan(&bitrate, &probed); err != nil {
		t.Fatal(err)
	}
	if bitrate != row.Bitrate || probed != row.Probed {
		t.Errorf("bitrate, probed = %d, %d, want %d, %d", bitrate, probed, row.Bitrate, row.Probed)
	}
}

// A file no probe has read carries a zero probe mark.
func TestAnUnprobedFileCarriesAZeroProbeMark(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)

	if _, err := catalog.UpsertFiles(t.Context(), []fileRow{{Library: "house/movies", Path: "One/one.mkv", Present: true}}); err != nil {
		t.Fatal(err)
	}

	var bitrate, probed int64
	if err := agent.db.QueryRow(`SELECT bitrate, probed FROM files WHERE library = ? AND path = ?`,
		"house/movies", "One/one.mkv").Scan(&bitrate, &probed); err != nil {
		t.Fatal(err)
	}
	if bitrate != 0 || probed != 0 {
		t.Errorf("bitrate, probed = %d, %d, want 0, 0", bitrate, probed)
	}
}

// A stream delete that fails stops the file sweep, so a file never leaves
// its streams behind.
func TestTheFileSweepStopsWhenTheStreamDeleteFails(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	recorder.status = 500

	if _, err := deleteFilesWithStreams(t.Context(), catalog, "house/movies", []string{"One/one.mkv"}); err == nil {
		t.Error("the file sweep hid a failed stream delete")
	}
}
