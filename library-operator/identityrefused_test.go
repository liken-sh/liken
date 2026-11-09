package main

// what these tests read: an id the identity fact cannot write into a title's
// .nfo file leaves an error attempt, so the error window asks again.

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

// A title folder that refuses the .nfo write keeps its .nfo file as it was,
// and the attempt is an error, whether the id came from TMDb's find call or
// from the identity ledger after a rewrite dropped it.
func TestAnIDTheNFOWillNotTakeIsAnError(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		folder string
		nfo    string
		ledger string
	}{
		{
			name:   "an id the find call named",
			kind:   libraryKindMovies,
			folder: "The Long Survey (1982)",
			nfo:    "<movie>\n  <title>The Long Survey</title>\n  <uniqueid type=\"imdb\">tt9000011</uniqueid>\n</movie>\n",
		},
		{
			name:   "an id the ledger holds",
			kind:   libraryKindSeries,
			folder: pineHollow,
			nfo:    sonarrSeriesNFO,
			ledger: foundLedger("113981"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				if os.Geteuid() == 0 {
					t.Skip("root writes into a read-only directory")
				}
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				folder := filepath.Join(root, test.folder)
				ledgers := filepath.Join(folder, likenDirectory)
				if err := os.MkdirAll(ledgers, 0o755); err != nil {
					t.Fatal(err)
				}
				if test.ledger != "" {
					writeFile(t, filepath.Join(ledgers, likenLedgerName(factIdentity)), test.ledger)
				}
				seedTitleWithNFO(t, catalog, test.kind, root, test.folder, test.nfo)
				nfoPath := filepath.Join(folder, movieNFOName)
				if test.kind == libraryKindSeries {
					nfoPath = filepath.Join(folder, seriesNFOName)
				}
				if err := os.Chmod(folder, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(folder, 0o755) })
				work, _ := testEnricher(t, test.kind, root, catalog)
				client, _ := newFakeTMDb(t, map[string]string{
					tmdbKey("/3/find/tt9000011", "", ""): `{"movie_results":[{"id":1101}],"tv_results":[]}`,
				})

				if err := work.identityGap(t.Context(), client); err != nil {
					t.Fatal(err)
				}

				if got := readFileString(t, nfoPath); got != test.nfo {
					t.Errorf("the .nfo file changed:\n%s", got)
				}
				ledger, err := readLikenLedger(folder, factIdentity)
				if err != nil {
					t.Fatal(err)
				}
				if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
					t.Errorf("attempts = %+v, want one error", ledger.Attempts)
				}
			})
		})
	}
}
