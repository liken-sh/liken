package main

import (
	"path/filepath"
	"testing"
)

// The ledger a folder carries in these tests, in the shape the identity
// fact writes when no rung parted two results.
const candidatesLedger = `items:
    - path: .
      candidates:
        - id: {tmdb: 11}
          title: Star Wars
          year: 1977
          receipt: {title: match, year: match}
attempts:
    - path: .
      at: 2026-09-02T14:00:00Z
      result: candidates
`

func TestAFolderWithAnIdentityLedgerYieldsAnAttemptsRow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	got := result.attempts[0]
	if got.Item != "movie:path:star-wars-1977" || got.Fact != factIdentity || got.Result != attemptCandidates {
		t.Errorf("attempt = %+v, want the item's own id under the identity fact", got)
	}
	if got.Library != "house/movies" || got.At != ledgerTime.Unix() {
		t.Errorf("attempt = %+v, want the library and the time the ledger records", got)
	}
}

func TestAProbeAttemptKeysOnTheFilePath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Star Wars (1977).mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	want := filepath.Join("Star Wars (1977)", "Star Wars (1977).mkv")
	if got := result.attempts[0]; got.Item != want || got.Fact != factProbe {
		t.Errorf("attempt = %+v, want the file path under the probe fact", got)
	}
}

func TestTheLikenDirectoryIsReadAsASidecarAndNeverAsATitle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkMovies(root, "house/movies", nil)

	if result.titles != 1 {
		t.Errorf("titles = %d, want the one title folder", result.titles)
	}
	for _, movie := range result.movies {
		if movie.Path == filepath.Join("Star Wars (1977)", likenDirectory) {
			t.Errorf("the walk read %s as a title", movie.Path)
		}
	}
	if len(result.attempts) != 1 {
		t.Errorf("attempts = %+v, want the sidecar read", result.attempts)
	}
}

func TestASeasonFolderLedgerKeysOnTheEpisodeItem(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Twin Peaks (1990)", "Season 01")
	writeFile(t, filepath.Join(season, "Twin Peaks - S01E01.mkv"), "video")
	writeFile(t, filepath.Join(season, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Twin Peaks - S01E01.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")
	writeFile(t, filepath.Join(season, likenDirectory, "identity.yaml"),
		"attempts:\n    - path: Twin Peaks - S01E01.mkv\n      at: 2026-09-02T14:00:00Z\n      result: nothing\n")

	result := walkSeries(root, "house/series", nil)

	items := map[string]string{}
	for _, attempt := range result.attempts {
		items[attempt.Fact] = attempt.Item
	}
	wantFile := filepath.Join("Twin Peaks (1990)", "Season 01", "Twin Peaks - S01E01.mkv")
	if items[factProbe] != wantFile {
		t.Errorf("the probe attempt names %q, want the file path", items[factProbe])
	}
	if items[factIdentity] != "episode:path:twin-peaks-1990:s01e01" {
		t.Errorf("the identity attempt names %q, want the episode item", items[factIdentity])
	}
}

func TestASeriesFolderLedgerNamesTheSeries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Twin Peaks (1990)")
	writeFile(t, filepath.Join(dir, "Season 01", "Twin Peaks - S01E01.mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), candidatesLedger)

	result := walkSeries(root, "house/series", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want one", result.attempts)
	}
	if got := result.attempts[0]; got.Item != "series:path:twin-peaks-1990" {
		t.Errorf("attempt = %+v, want the series item", got)
	}
}

func TestALedgerTheScannerCannotReadMarksThePassIncomplete(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"), "items: [oh: {: no\n")

	result := walkMovies(root, "house/movies", nil)

	if !result.readError {
		t.Error("the walk read as complete, want the incomplete mark")
	}
}

func TestAnAttemptWithNoResolvableItemIsLeftOut(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Star Wars (1977)")
	writeFile(t, filepath.Join(dir, "Star Wars (1977).mkv"), "video")
	writeFile(t, filepath.Join(dir, likenDirectory, "identity.yaml"),
		"attempts:\n    - path: a-file-that-left.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n"+
			"    - path: .\n      at: 2026-09-02T14:00:00Z\n      result: \n")

	result := walkMovies(root, "house/movies", nil)

	if len(result.attempts) != 0 {
		t.Errorf("attempts = %+v, want none", result.attempts)
	}
}

func TestAttemptKeysSplitBackIntoTheirColumns(t *testing.T) {
	keys := attemptKeys([]string{"movie:path:x" + linkKeySeparator + factIdentity, "no-separator"})

	if keys[0] != (attemptKey{Item: "movie:path:x", Fact: factIdentity}) {
		t.Errorf("key = %+v, want the item and the fact", keys[0])
	}
	if keys[1] != (attemptKey{Item: "no-separator"}) {
		t.Errorf("key = %+v, want the item alone", keys[1])
	}
}

// The probe records its attempt in the folder that holds the file it opened,
// so an extras folder under a series holds a ledger of its own.
func TestAnExtrasFolderLedgerKeysOnTheFilePath(t *testing.T) {
	root := t.TempDir()
	extras := filepath.Join(root, "Twin Peaks (1990)", "Extras")
	writeFile(t, filepath.Join(extras, "Deleted Scene.mkv"), "video")
	writeFile(t, filepath.Join(extras, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Deleted Scene.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")

	result := walkSeries(root, "house/series", nil)

	if len(result.attempts) != 1 {
		t.Fatalf("attempts = %+v, want the extras folder's own", result.attempts)
	}
	want := filepath.Join("Twin Peaks (1990)", "Extras", "Deleted Scene.mkv")
	if got := result.attempts[0]; got.Item != want || got.Fact != factProbe {
		t.Errorf("attempt = %+v, want the file path under the probe fact", got)
	}
}

// The next walk lifts the attempt into the catalog, and the gap query the
// container works from no longer names the file, so the probe opens it once.
func TestAnExtrasFileTheProbeOpenedIsNoLongerAGap(t *testing.T) {
	root := t.TempDir()
	extras := filepath.Join(root, "Twin Peaks (1990)", "Extras")
	writeFile(t, filepath.Join(extras, "Deleted Scene.mkv"), "video")
	writeFile(t, filepath.Join(extras, likenDirectory, "probe.yaml"),
		"attempts:\n    - path: Deleted Scene.mkv\n      at: 2026-09-02T14:00:00Z\n      result: found\n")
	catalog, _ := newSQLiteCatalog(t)
	if err := upsertWalk(t.Context(), catalog, walkSeries(root, "house/series", nil)); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.queryStrings(t.Context(), gapQueries[factProbe],
		[]any{"house/series", ledgerTime.Add(-defaultRetryInterval).Unix()})

	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none for a file the probe already opened", gaps)
	}
}
