package main

// What the walk and a fact's ledger write do with a media file that left its
// folder: the walk lifts none of its attempts, so the sweep takes their rows,
// and the fact drops its entries the next time it writes the ledger.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The name the episode of the fixture has after an upgrade to another
// release.
const upgradedEntry = "Quiet Harbor - S01E02 - Remux.mkv"

// upgrade removes the episode's video and writes another release of it
// under a new name, which is what an importer does on a quality upgrade.
func (e enrichedEpisode) upgrade(t *testing.T) {
	t.Helper()
	if err := os.Remove(e.video); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.season, upgradedEntry), "another release")
}

func TestAVideoThatIsGoneLiftsNoAttempt(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.upgrade(t)

	result := readFolder(episode.scan(), episode.series)

	if got := liftedFacts(result, episode.path); got != nil {
		t.Errorf("lifted %v, want no attempt at a path that holds no file", got)
	}
	for _, mark := range result.marks {
		if mark.Path == episode.path {
			t.Errorf("mark = %+v, want no span of a file that is gone", mark)
		}
	}
	if result.reopened.gone != 5 {
		t.Errorf("counted %d attempts of files that are gone, want the 5 of the fixture", result.reopened.gone)
	}
}

func TestARescanSweepsTheAttemptsOfAVideoThatIsGone(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)
	episode.rescan(t, catalog)
	episode.upgrade(t)

	episode.rescan(t, catalog)

	facts, err := catalog.queryStrings(t.Context(),
		`SELECT `+attemptFactColumn+` FROM attempts WHERE library = ? AND item = ?`,
		[]any{replacedLibrary, episode.path})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Errorf("the catalog holds the attempts %v, want none at a path that holds no file", facts)
	}
}

// A ledger of a file fact that holds entries for two videos, one of which is
// no longer in the folder.
func seedLedgerOfAGoneVideo(t *testing.T, fact string) string {
	t.Helper()
	folder := t.TempDir()
	writeFile(t, filepath.Join(folder, "kept.mkv"), "video")
	paths := []string{"kept.mkv", "gone.mkv"}
	ledger := likenLedger{}
	for _, path := range paths {
		ledger.Attempts = append(ledger.Attempts, likenAttempt{Path: path, At: ledgerTime, Result: attemptFound})
		ledger.Items = append(ledger.Items, likenItem{Path: path})
		ledger.Probes = append(ledger.Probes, probedFile{Path: path, At: ledgerTime})
		ledger.Marks = append(ledger.Marks, markEntry{Path: path, Kind: markKindIntro})
		ledger.Appearances = append(ledger.Appearances, appearancesEntry{Path: path})
		ledger.Files = append(ledger.Files, arrivalEntry{Path: path, At: ledgerTime})
	}
	writeFactLedger(t, folder, fact, ledger)
	return folder
}

// The paths a ledger holds, list by list.
func ledgerPaths(ledger likenLedger) map[string][]string {
	paths := map[string][]string{}
	for _, entry := range ledger.Attempts {
		paths["attempts"] = append(paths["attempts"], entry.Path)
	}
	for _, entry := range ledger.Items {
		paths["items"] = append(paths["items"], entry.Path)
	}
	for _, entry := range ledger.Probes {
		paths["probes"] = append(paths["probes"], entry.Path)
	}
	for _, entry := range ledger.Marks {
		paths["marks"] = append(paths["marks"], entry.Path)
	}
	for _, entry := range ledger.Appearances {
		paths["appearances"] = append(paths["appearances"], entry.Path)
	}
	for _, entry := range ledger.Files {
		paths["files"] = append(paths["files"], entry.Path)
	}
	return paths
}

func TestAFileFactsWriteDropsTheEntriesOfAVideoThatIsGone(t *testing.T) {
	for _, fact := range []string{factProbe, factArrival, factTrickplay, factEpisodeThumb, factMarks,
		factAppearances, factTitles} {
		t.Run(fact, func(t *testing.T) {
			folder := seedLedgerOfAGoneVideo(t, fact)

			err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, fact, func(*likenLedger) {})
			if err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(folder, fact)
			if err != nil {
				t.Fatal(err)
			}
			for list, paths := range ledgerPaths(ledger) {
				if !slices.Equal(paths, []string{"kept.mkv"}) {
					t.Errorf("%s holds %v, want the entry of the video in the folder alone", list, paths)
				}
			}
		})
	}
}

// A fact that keys on a title or on a file it writes itself names no media
// file, so its write keeps every entry: a poster the provider did not hold
// is a miss at a path with no file.
func TestAnotherFactsWriteKeepsEveryEntry(t *testing.T) {
	for _, fact := range []string{factIdentity, factPoster, factTrailerFile} {
		t.Run(fact, func(t *testing.T) {
			folder := seedLedgerOfAGoneVideo(t, fact)

			err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, fact, func(*likenLedger) {})
			if err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(folder, fact)
			if err != nil {
				t.Fatal(err)
			}
			if got := ledgerPaths(ledger)["attempts"]; !slices.Equal(got, []string{"kept.mkv", "gone.mkv"}) {
				t.Errorf("attempts hold %v, want both entries", got)
			}
		})
	}
}
