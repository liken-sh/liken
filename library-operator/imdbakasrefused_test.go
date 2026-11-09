package main

// what these tests read: the alternate-titles fact leaves a title's gap open
// when the catalog refuses a read, when the title is outside a narrowed Job,
// and when its folder refuses the ledger, and the walk drops an alternate
// that the title the .nfo file names now says.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A read the catalog refuses ends the fact with the refusal, and the title
// gets no ledger, so its gap stays open for the next run. refused counts the
// reads the catalog answers before it refuses.
func TestACatalogThatRefusesTheAlternateTitlesReadsLeavesTheGap(t *testing.T) {
	cases := []struct {
		name    string
		refused int
	}{
		{name: "the gap", refused: 0},
		{name: "the IMDb ids", refused: 1},
		{name: "the title", refused: 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				work, catalog, agent, _, root := imdbEnricherOnAgent(t, libraryKindMovies)
				nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
				agent.queriesLeft = one.refused + 1

				if err := work.alternateTitlesFact(t.Context()); err == nil {
					t.Error("the fact reported no error, want the refusal")
				}
				if attempts := attemptsBeside(t, nfoPath, factAlternateTitles); len(attempts) != 0 {
					t.Errorf("attempts = %+v, want none", attempts)
				}
			})
		})
	}
}

// A Job narrowed to one folder, as a webhook's rescan is, writes the
// alternate titles of that folder alone.
func TestANarrowedJobWritesNoAlternateTitlesOutsideItsScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
		nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
		work.scopes = []string{"Another Film (2012)"}

		runAlternateTitles(t, work)

		if attempts := attemptsBeside(t, nfoPath, factAlternateTitles); len(attempts) != 0 {
			t.Errorf("attempts = %+v, want none outside the scope", attempts)
		}
		if got := server.log(); !slices.Equal(got, []string{"GET title.akas 200"}) {
			t.Errorf("requests = %v, want the one read of title.akas", got)
		}
	})
}

// A folder that refuses the ledger keeps no record of the attempt, and the
// log names the write's error.
func TestAnAlternateTitlesLedgerTheFolderRefusesIsLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes into a read-only directory")
		}
		work, catalog, _, root := imdbEnricher(t, libraryKindMovies)
		nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
		if err := os.Chmod(filepath.Dir(nfoPath), 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Dir(nfoPath), 0o755) })

		runAlternateTitles(t, work)

		if _, err := os.Stat(filepath.Join(filepath.Dir(nfoPath), likenDirectory)); !os.IsNotExist(err) {
			t.Errorf("stat .liken = %v, want no ledger directory", err)
		}
		if log := work.log.(*bytes.Buffer).String(); !strings.Contains(log, "could not record the alternate-titles of") {
			t.Errorf("log = %q, want the write's error", log)
		}
	})
}

// The walk reads the ledger's titles against the title the .nfo file names
// now, which can change after the fact ran, so a title the .nfo file has
// taken is no longer an alternate. A title with no letters or digits
// matches no query, and a repeat adds nothing.
func TestTheWalkDropsTheAlternatesTheTitleNowSays(t *testing.T) {
	cases := []struct {
		name    string
		entries []alternateTitle
		title   string
		want    []string
	}{
		{name: "the title the .nfo file took",
			entries: []alternateTitle{{Title: "Port d'hiver", Original: true}, {Title: "Winter Harbor"}},
			title:   "Winter Harbor", want: []string{"Port d'hiver"}},
		{name: "a title of marks alone",
			entries: []alternateTitle{{Title: "!!!"}, {Title: "Harbour"}},
			title:   "Winter Harbour", want: []string{"Harbour"}},
		{name: "a repeat in another case",
			entries: []alternateTitle{{Title: "Harbour"}, {Title: "HARBOUR"}},
			title:   "Winter Harbour", want: []string{"Harbour"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := alternatesOf(one.entries, one.title); !slices.Equal(got, one.want) {
				t.Errorf("alternates = %v, want %v", got, one.want)
			}
		})
	}
}
