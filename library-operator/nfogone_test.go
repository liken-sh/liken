package main

// What an nfo fact does when its group is gone because the whole .nfo file
// went. Radarr deletes movie.nfo when it upgrades a title's video, and when
// its own metadata write fails it leaves no file in its place. The probe or
// the identity fact then writes a minimal file, so every group is empty and
// no writer holds one.

import (
	"os"
	"path/filepath"
	"testing"
)

// The two states the file reaches after the delete: no file yet, and the
// minimal file the identity fact writes when it puts the ids back.
var goneNFOStates = []struct {
	name    string
	rebuild func(t *testing.T, work *enricher, nfoPath string)
}{
	{name: "no file", rebuild: func(*testing.T, *enricher, string) {}},
	{name: "a minimal file with the ids", rebuild: func(t *testing.T, work *enricher, nfoPath string) {
		if err := work.writeUniqueID(nfoPath, nfoRootMovie, "Winter Harbour", "tmdb", "4242"); err != nil {
			t.Fatal(err)
		}
	}},
}

func TestAFactWritesItsGroupAgainAfterTheNFOFileWasDeleted(t *testing.T) {
	for _, state := range goneNFOStates {
		for _, fact := range nfoFacts {
			t.Run(state.name+"/"+fact, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := t.TempDir()
				folder := "Winter Harbour (2011)"
				nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
				work, _ := testEnricher(t, libraryKindMovies, root, catalog)
				fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}
				if err := work.nfoGap(t.Context(), fact, lineOf(fake)); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(nfoPath); err != nil {
					t.Fatal(err)
				}
				state.rebuild(t, work, nfoPath)
				item, _, err := catalog.identityItem(t.Context(), "house/movies", "movie:tmdb:4242")
				if err != nil {
					t.Fatal(err)
				}

				got, _ := work.fillNFOFact(t.Context(), fact, lineOf(fake), item)

				if got != attemptFound {
					t.Errorf("the rerun answered %q, want found, because no writer holds an empty group", got)
				}
				hash, err := groupHash([]byte(readFileString(t, nfoPath)), nfoGroup(fact))
				if err != nil {
					t.Fatal(err)
				}
				if hash == "" {
					t.Errorf("the .nfo file holds no %s group after the rerun:\n%s", fact, readFileString(t, nfoPath))
				}
				ledger, err := readLikenLedger(filepath.Join(root, folder), fact)
				if err != nil {
					t.Fatal(err)
				}
				if len(ledger.Items) != 1 || ledger.Items[0].Wrote != hash {
					t.Errorf("items = %+v, want one with the hash of the group the rerun wrote", ledger.Items)
				}
			})
		}
	}
}
