package main

// These tests prove the mark-and-sweep reconciliation against the stateful
// fake catalog: a full walk marks what it read and prunes what it did not,
// a partial walk keeps the catalog, a webhook prunes one folder, and the
// prune never reaches another library's rows.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedTitle writes one movie, its file, and its alias into the catalog the
// real way, through the write client, so a prune test starts from a catalog
// a walk could have written.
func seedTitle(t *testing.T, catalog *Catalog, library, id, path string) {
	t.Helper()
	ctx := context.Background()
	if _, err := catalog.UpsertMovies(ctx, []movieRow{{Id: id, Library: library, Path: path, Title: id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertFiles(ctx, []fileRow{{Path: path + "/movie.mkv", Library: library, Present: true, Items: []string{id}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertFileItems(ctx, []fileRow{{Path: path + "/movie.mkv", Items: []string{id}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertAliases(ctx, []aliasRow{{Alias: id, Item: id, Source: aliasSourceProvider}}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneLibraryDeletesTheUnmarkedRows(t *testing.T) {
	catalog, fake := newFakeCatalog(t)
	ctx := context.Background()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	seedTitle(t, catalog, "house/movies", "movie:tmdb:1", "One")
	seedTitle(t, catalog, "house/movies", "movie:tmdb:2", "Two")

	// A later walk read only the first title, so only it carries the epoch.
	epoch := int64(1000)
	marked := []string{seenItem + "movie:tmdb:1", seenFile + "One/movie.mkv", seenAlias + "movie:tmdb:1"}
	if _, err := catalog.markSeen(ctx, marked, epoch); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneLibrary(ctx, catalog, "house/movies", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want the departed movie, its file, and its alias", removed)
	}
	movies := fake.held(fake.movies)
	if len(movies) != 1 || movies["movie:tmdb:1"].path != "One" {
		t.Errorf("movies = %v, want only the marked title", movies)
	}
	if len(fake.held(fake.files)) != 1 {
		t.Errorf("files = %v, want only the marked file", fake.held(fake.files))
	}
}

func TestPruneLibraryKeepsAnotherLibrarysRows(t *testing.T) {
	catalog, fake := newFakeCatalog(t)
	ctx := context.Background()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	seedTitle(t, catalog, "house/movies", "movie:tmdb:1", "One")
	seedTitle(t, catalog, "studio/films", "movie:tmdb:9", "Nine")

	// No id carries this epoch, so a library-blind prune would delete both.
	// The prune scopes to its library, so the other library's row stands.
	removed, err := pruneLibrary(ctx, catalog, "house/movies", int64(2000))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want only this library's rows", removed)
	}
	movies := fake.held(fake.movies)
	if _, held := movies["movie:tmdb:9"]; !held {
		t.Errorf("movies = %v, want the other library's row kept", movies)
	}
	if _, held := movies["movie:tmdb:1"]; held {
		t.Errorf("movies = %v, want this library's unmarked row gone", movies)
	}
}

func TestPruneScopeDeletesOnlyTheFolder(t *testing.T) {
	catalog, fake := newFakeCatalog(t)
	ctx := context.Background()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	seedTitle(t, catalog, "house/movies", "movie:tmdb:1", "Action/One")
	seedTitle(t, catalog, "house/movies", "movie:tmdb:2", "Action/Two")

	// The webhook named the first folder, which left the volume, so the
	// rescan marks nothing and the scoped prune removes only its rows.
	removed, err := pruneScope(ctx, catalog, "house/movies", "Action/One", int64(3000))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want only the named folder's rows", removed)
	}
	movies := fake.held(fake.movies)
	if _, held := movies["movie:tmdb:2"]; !held {
		t.Errorf("movies = %v, want the other folder kept", movies)
	}
	if _, held := movies["movie:tmdb:1"]; held {
		t.Errorf("movies = %v, want the named folder gone", movies)
	}
}

func TestIncompleteWalk(t *testing.T) {
	cases := []struct {
		name         string
		readError    bool
		items        int
		catalogItems int
		want         bool
	}{
		{name: "a read error at the root is incomplete", readError: true, items: 0, catalogItems: 100, want: true},
		{name: "far below the catalog is incomplete", items: 10, catalogItems: 100, want: true},
		{name: "above half is complete", items: 60, catalogItems: 100, want: false},
		{name: "a small catalog skips the ratio guard", items: 0, catalogItems: 5, want: false},
		{name: "a first walk of an empty catalog is complete", items: 3, catalogItems: 0, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := incompleteWalk(testCase.readError, testCase.items, testCase.catalogItems); got != testCase.want {
				t.Errorf("incompleteWalk = %v, want %v", got, testCase.want)
			}
		})
	}
}

// Every key carries the prefix of its own key space. The movie's id and the
// alias of the same name are two keys and not one, which is what keeps an
// alias from marking a stale item row and holding it in the catalog.
func TestMarkKeysReadsEveryIdPathAndAliasInItsOwnKeySpace(t *testing.T) {
	result := &walkResult{
		movies:   []movieRow{{Id: "movie:tmdb:1"}},
		series:   []seriesRow{{Id: "series:tvdb:1"}},
		episodes: []episodeRow{{Id: "episode:tvdb:1:s01e01"}},
		files:    []fileRow{{Path: "one.mkv"}},
		aliases:  []aliasRow{{Alias: "movie:imdb:tt1"}, {Alias: "movie:tmdb:1"}},
	}
	keys := markKeys(result)
	want := map[string]bool{
		seenItem + "movie:tmdb:1":          true,
		seenItem + "series:tvdb:1":         true,
		seenItem + "episode:tvdb:1:s01e01": true,
		seenFile + "one.mkv":               true,
		seenAlias + "movie:imdb:tt1":       true,
		seenAlias + "movie:tmdb:1":         true,
	}
	if len(keys) != len(want) {
		t.Errorf("keys = %v, want %d keys", keys, len(want))
	}
	for _, key := range keys {
		if !want[key] {
			t.Errorf("keys = %v, holds an unexpected key %q", keys, key)
		}
	}
}

// fakeScanner builds a scanner over a root wired to the stateful fake
// catalog and a bus that never connects, so a full walk runs the whole
// mark-and-sweep with no cluster and no broker.
func fakeScanner(t *testing.T, root, kind string) (*scanner, *fakeCatalog) {
	t.Helper()
	catalog, fake := newFakeCatalog(t)
	scan := &scanner{
		root:    root,
		library: "house/movies",
		kind:    kind,
		catalog: catalog,
		report:  libraryReport{LastWalk: time.Now().UTC(), LastChange: time.Now().UTC()},
		bus:     newBus("", "test", nil, nil, nil),
	}
	return scan, fake
}

// titleTree writes a movie library of title folders under a temp dir, each
// a folder with one video file, so a walk reads them as titles.
func titleTree(t *testing.T, folders ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, folder := range folders {
		dir := filepath.Join(root, folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFullWalkPrunesADepartedTitleAndCountsIt(t *testing.T) {
	root := titleTree(t, "A (2001)", "B (2002)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if len(fake.held(fake.movies)) != 2 {
		t.Fatalf("movies = %v, want the two titles the first walk read", fake.held(fake.movies))
	}

	// The second title left the volume, so the next walk prunes it and the
	// report carries the count of rows that left.
	if err := os.RemoveAll(filepath.Join(root, "B (2002)")); err != nil {
		t.Fatal(err)
	}
	scan.fullWalk(ctx)

	movies := fake.held(fake.movies)
	if len(movies) != 1 {
		t.Fatalf("movies = %v, want only the surviving title", movies)
	}
	if scan.report.RemovedLastSweep != 3 {
		t.Errorf("removedLastSweep = %d, want the departed movie, file, and alias", scan.report.RemovedLastSweep)
	}
}

// The full walk buffers the streamed folders and flushes each buffer when it
// reaches scanFlushBatch. A low threshold drives several flushes over a small
// set, and the count of flushes scales with the threshold, so the walk holds one
// batch and never the whole library. A batch of one flushes every folder alone,
// whatever the library's size, which is the bound the design promises.
func TestFullWalkFlushesInBoundedChunks(t *testing.T) {
	cases := []struct {
		name        string
		batch       int
		wantFlushes int
	}{
		{name: "one folder per flush", batch: 1, wantFlushes: 5},
		{name: "two folders per flush", batch: 2, wantFlushes: 3},
		{name: "one flush holds the whole small set", batch: 100, wantFlushes: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := titleTree(t, "A (2001)", "B (2002)", "C (2003)", "D (2004)", "E (2005)")
			scan, fake := fakeScanner(t, root, libraryKindMovies)

			batchWas := scanFlushBatch
			t.Cleanup(func() { scanFlushBatch = batchWas })
			scanFlushBatch = testCase.batch

			scan.fullWalk(context.Background())

			if got := len(fake.held(fake.movies)); got != 5 {
				t.Fatalf("movies = %d, want the five titles the walk read", got)
			}
			if got := fake.flushes(); got != testCase.wantFlushes {
				t.Errorf("flushes = %d, want %d for a batch of %d", got, testCase.wantFlushes, testCase.batch)
			}
		})
	}
}

func TestFullWalkSkipsThePruneOnAnUnreadableRoot(t *testing.T) {
	root := titleTree(t, "A (2001)", "B (2002)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	var logged bytes.Buffer
	scan.log = &logged
	ctx := context.Background()

	scan.fullWalk(ctx)
	if len(fake.held(fake.movies)) != 2 {
		t.Fatalf("movies = %v, want the two titles", fake.held(fake.movies))
	}
	if scan.report.Titles != 2 {
		t.Fatalf("titles = %d, want the two titles the first walk read", scan.report.Titles)
	}

	// The root became unreadable, so the walk read nothing. The prune is
	// skipped and the catalog stands.
	scan.root = filepath.Join(root, "gone")
	scan.fullWalk(ctx)

	if len(fake.held(fake.movies)) != 2 {
		t.Errorf("movies = %v, want the catalog kept after a partial read", fake.held(fake.movies))
	}
	if scan.report.RemovedLastSweep != 0 {
		t.Errorf("removedLastSweep = %d, want no sweep on a partial read", scan.report.RemovedLastSweep)
	}
	if scan.report.Titles != 2 {
		t.Errorf("titles = %d, want the last good count kept after an incomplete walk", scan.report.Titles)
	}
	if !strings.Contains(logged.String(), "incomplete walk") {
		t.Errorf("log = %q, want the incomplete walk logged", logged.String())
	}
}

// a finished walk logs one summary line with the counts and the
// duration, and names the folders it could not identify up to the cap, then
// tallies the rest, so a large library logs a sample and never one line per
// folder.
func TestFullWalkLogsASummaryAndACappedUnidentifiedSample(t *testing.T) {
	cases := []struct {
		name    string
		count   int
		wantAll bool
	}{
		{name: "a short list names every folder", count: 3, wantAll: true},
		{name: "a long list names the cap and tallies the rest", count: 12, wantAll: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			folders := make([]string, 0, testCase.count)
			for i := range testCase.count {
				folders = append(folders, fmt.Sprintf("Mystery %c", 'A'+i))
			}
			root := titleTree(t, folders...)
			scan, _ := fakeScanner(t, root, libraryKindMovies)
			var logged bytes.Buffer
			scan.log = &logged

			scan.fullWalk(context.Background())

			out := logged.String()
			if !strings.Contains(out, fmt.Sprintf("walking %s", root)) {
				t.Errorf("log = %q, want the walk-start line", out)
			}
			summary := fmt.Sprintf("walk complete: %d titles, %d unidentified, 0 removed", testCase.count, testCase.count)
			if !strings.Contains(out, summary) {
				t.Errorf("log = %q, want %q", out, summary)
			}
			if testCase.wantAll && strings.Contains(out, "more") {
				t.Errorf("log = %q, want every folder named with no tally", out)
			}
			if !testCase.wantAll && !strings.Contains(out, fmt.Sprintf("and %d more", testCase.count-unidentifiedSample)) {
				t.Errorf("log = %q, want the tally of the folders past the cap", out)
			}
		})
	}
}

// a walk that reads far fewer items than the catalog holds is
// incomplete, so it keeps the last good count rather than overwrite it with
// the low one, and it names the shortfall in the log.
func TestFullWalkKeepsTheCountWhenTheWalkFallsFarShort(t *testing.T) {
	folders := make([]string, 0, 20)
	for i := range 20 {
		folders = append(folders, fmt.Sprintf("Title %02d (20%02d)", i, i))
	}
	root := titleTree(t, folders...)
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	var logged bytes.Buffer
	scan.log = &logged
	ctx := context.Background()

	scan.fullWalk(ctx)
	if scan.report.Titles != 20 {
		t.Fatalf("titles = %d, want the twenty titles the first walk read", scan.report.Titles)
	}

	// all but two folders vanished at once, far below half the
	// catalog, so the walk is read as incomplete and the count stands.
	for _, folder := range folders[2:] {
		if err := os.RemoveAll(filepath.Join(root, folder)); err != nil {
			t.Fatal(err)
		}
	}
	scan.fullWalk(ctx)

	if scan.report.Titles != 20 {
		t.Errorf("titles = %d, want the last good count kept after a walk that fell far short", scan.report.Titles)
	}
	if len(fake.held(fake.movies)) != 20 {
		t.Errorf("movies = %d, want the catalog kept after an incomplete walk", len(fake.held(fake.movies)))
	}
	if !strings.Contains(logged.String(), "of 20 cataloged") {
		t.Errorf("log = %q, want the shortfall named", logged.String())
	}
}

func TestFullWalkLeavesTheReportOnAWriteError(t *testing.T) {
	root := titleTree(t, "A (2001)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	fake.failStatus = 500

	scan.fullWalk(context.Background())

	if scan.report.Titles != 0 {
		t.Errorf("titles = %d, want the report untouched after a write error", scan.report.Titles)
	}
}

// a prune read that misses the freshly created seen table must not
// discard the walk's title count. The walk read and wrote the volume, so the
// report carries the count and the failed prune is logged and left for the
// next walk, in place of a report stuck at zero for a whole scanInterval.
func TestFullWalkReportsTitlesWhenThePruneReadMissesSeen(t *testing.T) {
	root := titleTree(t, "A (2001)", "B (2002)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	var logged bytes.Buffer
	scan.log = &logged
	fake.seenLagReads = 1

	scan.fullWalk(context.Background())

	if scan.report.Titles != 2 {
		t.Errorf("titles = %d, want the two titles the walk read despite the prune read miss", scan.report.Titles)
	}
	if len(fake.held(fake.movies)) != 2 {
		t.Errorf("movies = %v, want the two titles the walk wrote", fake.held(fake.movies))
	}
	if !strings.Contains(logged.String(), "seen") {
		t.Errorf("log = %q, want the failed prune logged", logged.String())
	}
}

// the second count read tells a change from no change, and it reads
// the catalog back through the query API, so it can lag too. A failure there
// still leaves the walk's title count in the report and is logged, in place
// of a report stuck at zero.
func TestFullWalkReportsTitlesWhenTheAfterCountFails(t *testing.T) {
	root := titleTree(t, "A (2001)", "B (2002)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	var logged bytes.Buffer
	scan.log = &logged
	fake.countErrorsAfter = 1

	scan.fullWalk(context.Background())

	if scan.report.Titles != 2 {
		t.Errorf("titles = %d, want the two titles the walk read despite the after-count failing", scan.report.Titles)
	}
	if !strings.Contains(logged.String(), "count") {
		t.Errorf("log = %q, want the failed count logged", logged.String())
	}
}

// artTitleTree writes a movie library whose title folders carry the art and the
// subtitle beside the video, so a prune test reads a title of more than one
// file.
func artTitleTree(t *testing.T, folders ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, folder := range folders {
		dir := filepath.Join(root, folder)
		writeFile(t, filepath.Join(dir, "movie.mkv"), "video")
		writeFile(t, filepath.Join(dir, "folder.jpg"), "image")
		writeFile(t, filepath.Join(dir, "movie.en.srt"), "subtitle")
	}
	return root
}

// The counts the status reports are the catalog's own, read after the
// prune, and not the number of rows the walk wrote.
func TestFullWalkReportsTheCatalogsItemAndFileCounts(t *testing.T) {
	root := artTitleTree(t, "A (2001)", "B (2002)")
	scan, _ := fakeScanner(t, root, libraryKindMovies)

	scan.fullWalk(context.Background())

	if scan.report.Items != 2 {
		t.Errorf("items = %d, want the two movie rows the catalog holds", scan.report.Items)
	}
	if scan.report.Files != 6 {
		t.Errorf("files = %d, want the three file rows of each title", scan.report.Files)
	}
}

// A count read that fails leaves the count at its last value, the way
// the walk keeps its last report when a catalog step fails.
func TestFullWalkKeepsTheLastCountsWhenACountReadFails(t *testing.T) {
	root := artTitleTree(t, "A (2001)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	var logged bytes.Buffer
	scan.log = &logged
	ctx := context.Background()

	scan.fullWalk(ctx)
	if scan.report.Items != 1 || scan.report.Files != 3 {
		t.Fatalf("counts = %d items and %d files, want the catalog's own counts", scan.report.Items, scan.report.Files)
	}

	// The next walk's first count read succeeds, and the two reads after
	// the prune fail.
	fake.failCountsAfter(fake.counts() + 1)
	scan.fullWalk(ctx)

	if scan.report.Items != 1 || scan.report.Files != 3 {
		t.Errorf("counts = %d items and %d files, want the last walk's counts kept", scan.report.Items, scan.report.Files)
	}
	if !strings.Contains(logged.String(), "count the catalog after the walk") {
		t.Errorf("log = %q, want the failed item count logged", logged.String())
	}
	if !strings.Contains(logged.String(), "count the catalog's files") {
		t.Errorf("log = %q, want the failed file count logged", logged.String())
	}
}

// The mark-and-sweep treats a poster as it treats a video, so a poster a
// person deletes leaves the catalog on the next walk, and the prune needed no
// change to do it.
func TestFullWalkPrunesADeletedPoster(t *testing.T) {
	root := artTitleTree(t, "A (2001)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if len(fake.held(fake.files)) != 3 {
		t.Fatalf("files = %v, want the video, the poster, and the subtitle", fake.held(fake.files))
	}

	if err := os.Remove(filepath.Join(root, "A (2001)", "folder.jpg")); err != nil {
		t.Fatal(err)
	}
	scan.fullWalk(ctx)

	files := fake.held(fake.files)
	if _, held := files[filepath.Join("A (2001)", "folder.jpg")]; held {
		t.Errorf("files = %v, want the deleted poster gone", files)
	}
	if len(files) != 2 {
		t.Errorf("files = %v, want the video and the subtitle kept", files)
	}
	if scan.report.RemovedLastSweep != 1 {
		t.Errorf("removedLastSweep = %d, want the one poster row", scan.report.RemovedLastSweep)
	}
}

// A webhook rescan reconciles within the folder it names. One title's files
// leave the catalog, and another title's stay.
func TestRescanPrunesOnlyTheNamedTitlesFiles(t *testing.T) {
	root := artTitleTree(t, "A (2001)", "B (2002)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if len(fake.held(fake.files)) != 6 {
		t.Fatalf("files = %v, want three files for each of the two titles", fake.held(fake.files))
	}

	if err := os.Remove(filepath.Join(root, "A (2001)", "folder.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "A (2001)", "movie.en.srt")); err != nil {
		t.Fatal(err)
	}
	scan.rescan(ctx, filepath.Join(root, "A (2001)"))

	files := fake.held(fake.files)
	kept := []string{
		filepath.Join("A (2001)", "movie.mkv"),
		filepath.Join("B (2002)", "movie.mkv"),
		filepath.Join("B (2002)", "folder.jpg"),
		filepath.Join("B (2002)", "movie.en.srt"),
	}
	for _, path := range kept {
		if _, held := files[path]; !held {
			t.Errorf("files = %v, want %q kept", files, path)
		}
	}
	if len(files) != len(kept) {
		t.Errorf("files = %v, want only the named title's departed files pruned", files)
	}
}

func TestRescanRemovesAVanishedFolder(t *testing.T) {
	root := titleTree(t, "A (2001)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	ctx := context.Background()

	scan.fullWalk(ctx)
	if len(fake.held(fake.movies)) != 1 {
		t.Fatalf("movies = %v, want the one title", fake.held(fake.movies))
	}

	// The folder left the volume and a webhook named it, so the rescan
	// deletes its rows without waiting for the slow walk.
	if err := os.RemoveAll(filepath.Join(root, "A (2001)")); err != nil {
		t.Fatal(err)
	}
	scan.rescan(ctx, filepath.Join(root, "A (2001)"))

	if len(fake.held(fake.movies)) != 0 {
		t.Errorf("movies = %v, want the vanished folder gone", fake.held(fake.movies))
	}
}

// A title that gains a provider id changes its canonical id, and its old
// path-derived id becomes an alias of the new one. The stale item row must
// still leave the catalog. The two carry the same string, so with one key
// space the alias marks the stale row every walk and the prune never removes
// it, and the catalog holds the title twice for as long as the folder stands.
func TestAWalkPrunesATitleThatGainedAProviderID(t *testing.T) {
	root := artTitleTree(t, "Red Sonja (1985)")
	scan, fake := fakeScanner(t, root, libraryKindMovies)
	ctx := context.Background()

	scan.fullWalk(ctx)
	pathID := "movie:path:" + slug("Red Sonja (1985)", 0)
	if _, held := fake.held(fake.movies)[pathID]; !held {
		t.Fatalf("movies = %v, want the title under its path-derived id", fake.held(fake.movies))
	}

	// The sidecar arrives, so the walk reads a provider id and the title's
	// canonical id becomes the tmdb one.
	writeFile(t, filepath.Join(root, "Red Sonja (1985)", "movie.nfo"),
		`<movie><title>Red Sonja</title><year>1985</year><uniqueid type="tmdb">9760</uniqueid></movie>`)
	scan.fullWalk(ctx)

	movies := fake.held(fake.movies)
	if _, held := movies[pathID]; held {
		t.Errorf("movies = %v, want the stale path-derived row pruned", movies)
	}
	if _, held := movies["movie:tmdb:9760"]; !held {
		t.Errorf("movies = %v, want the title under its provider id", movies)
	}
	if len(movies) != 1 {
		t.Errorf("movies = %v, want one row for one folder", movies)
	}
	if scan.report.Items != 1 {
		t.Errorf("items = %d, want the one title the volume holds", scan.report.Items)
	}
	// The path-derived id stays an alias, so a reader that holds the old id
	// still resolves the title.
	if item := fake.aliases[pathID]; item != "movie:tmdb:9760" {
		t.Errorf("alias %s resolves to %q, want the provider id", pathID, item)
	}
}
