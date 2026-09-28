package main

// what these tests read: a fact whose row write the catalog refuses logs the
// refusal and ends its write there, and the run goes on, because the files
// hold the truth and the next walk writes the same rows.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// one movie folder with a video, a poster, an .nfo file, and an overview
// attempt, so every fact below has rows to write.
func refusedRowsFolder(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, "Winter Harbour (2011)")
	writeFile(t, filepath.Join(folder, "Winter Harbour (2011).mkv"), "video")
	writeFile(t, filepath.Join(folder, "poster.jpg"), "image")
	writeFile(t, filepath.Join(folder, movieNFOName), `<movie><title>Winter Harbour</title>`+
		`<uniqueid type="tmdb">4242</uniqueid></movie>`)
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(factOverview)),
		"attempts:\n  - path: .\n    at: 2026-09-01T00:00:00Z\n    result: found\n")
	return root, folder
}

func TestARefusedRowWriteIsLoggedAndTheRunGoesOn(t *testing.T) {
	cases := []struct {
		fact    string
		refused int
	}{
		{fact: factProbe, refused: 1},
		{fact: factProbe, refused: 2},
		{fact: factArrival, refused: 1},
		{fact: factArrival, refused: 2},
		{fact: factCredits, refused: 1},
		{fact: factCredits, refused: 2},
		{fact: factTrailer, refused: 1},
		{fact: factPoster, refused: 1},
		{fact: factPoster, refused: 2},
		{fact: factPoster, refused: 3},
		{fact: factOverview, refused: 1},
		{fact: factOverview, refused: 2},
	}
	for _, one := range cases {
		t.Run(fmt.Sprintf("%s write %d", one.fact, one.refused), func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			root, folder := refusedRowsFolder(t)
			work, log := testEnricher(t, libraryKindMovies, root, catalog)
			agent.transactionsLeft = one.refused

			work.writeRows(one.fact, folder, true)

			if !strings.Contains(log.String(), "could not write the "+one.fact) {
				t.Errorf("log = %q, want the refused write", log.String())
			}
		})
	}
}
