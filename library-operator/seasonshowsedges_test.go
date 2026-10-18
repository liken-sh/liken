package main

// The edges of the season mapping: a season.nfo the walk cannot open, the
// season art names an entry carries, the replace door of an art file of
// another show, and the log line of a skipped <showid>.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// unreadableSeasonNFO puts a directory where the fixture's season.nfo goes,
// which no read can open as a file.
func (e enrichedEpisode) unreadableSeasonNFO(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(e.season, seasonNFOName), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A season.nfo the walk cannot open marks the pass incomplete, so the sweep
// removes no row the volume may still hold.
func TestAnUnreadableSeasonNFOMarksTheWalkIncomplete(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.unreadableSeasonNFO(t)

	result := readFolder(episode.scan(), episode.series)

	if !result.readError {
		t.Error("the walk read the folder as whole, want it incomplete")
	}
}

// A season art fact and the marks fact record an error for a season whose
// mapping they cannot read, and ask no provider.
func TestAFactThatCannotReadTheMappingRecordsAnError(t *testing.T) {
	cases := []struct {
		name   string
		run    func(work *enricher, e enrichedEpisode)
		folder func(e enrichedEpisode) string
		fact   string
		entry  string
	}{
		{name: "a season's poster", fact: factSeasonPoster, entry: "season01-poster.jpg",
			run: func(work *enricher, e enrichedEpisode) {
				work.artOne(t.Context(), artLineOf(), artTypes[factSeasonPoster],
					artGap{key: filepath.Join(replacedSeries, "season01-poster.jpg"), tmdb: "2001", season: 1})
			},
			folder: func(e enrichedEpisode) string { return e.series }},
		{name: "an episode's marks", fact: factMarks, entry: replacedEntry,
			run: func(work *enricher, e enrichedEpisode) {
				work.marksOne(t.Context(), markLineOf(), e.path, markFile{episodes: 1, season: 1, episode: 2})
			},
			folder: func(e enrichedEpisode) string { return e.season }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				episode := seedEnrichedEpisode(t)
				episode.unreadableSeasonNFO(t)
				work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

				test.run(work, episode)

				ledger := artLedger(t, test.folder(episode), test.fact)
				if attempt := attemptAt(ledger, test.entry); attempt.Result != attemptError {
					t.Errorf("the attempt is %+v, want an error", attempt)
				}
			})
		})
	}
}

// An episode's still whose ledger the fact cannot read records an error, and
// the still stays as it is.
func TestAStillWhoseLedgerCannotBeReadStays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		episode := seedEnrichedEpisode(t)
		ledger := filepath.Join(episode.season, likenDirectory, likenLedgerName(factEpisodeThumb))
		writeFile(t, ledger, "items: [not, a: ledger")
		work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

		work.artOne(t.Context(), artLineOf(), artTypes[factEpisodeThumb],
			artGap{key: episode.path, tmdb: "2001", season: 1, episode: 2})

		if got := readFileString(t, episode.thumb); got != "a still of the first encode" {
			t.Errorf("the thumbnail holds %q, want it kept", got)
		}
	})
}

func TestASeasonArtEntryNamesItsSeason(t *testing.T) {
	shows := folderShows{folder: "tmdb:1:1", seasons: map[int]string{0: "tmdb:2:1", 3: "tmdb:3:1"}}
	cases := []struct {
		fact  string
		entry string
		want  string
	}{
		{fact: factSeasonPoster, entry: "season03-poster.jpg", want: "tmdb:3:1"},
		{fact: factSeasonBanner, entry: specialsSeasonPrefix + "banner.jpg", want: "tmdb:2:1"},
		{fact: factSeasonPoster, entry: "poster.jpg", want: ""},
		{fact: factEpisodeThumb, entry: replacedEntry, want: "tmdb:1:1"},
		{fact: factPoster, entry: "poster.jpg", want: ""},
	}
	for _, test := range cases {
		t.Run(test.fact+" "+test.entry, func(t *testing.T) {
			if got := shows.of(test.fact, test.entry); got != test.want {
				t.Errorf("of = %q, want %q", got, test.want)
			}
		})
	}
}

// The door that replaces an art file of another show creates a file that is
// gone, and keeps anything that is no file.
func TestTheOwnArtDoorCreatesAndKeeps(t *testing.T) {
	root := t.TempDir()
	writer := newVolumeWriter("job")
	written := time.Now()

	created := filepath.Join(root, "season02-poster.jpg")
	if wrote, err := writer.replaceOwnFile(created, []byte("new"), written); err != nil || !wrote {
		t.Errorf("wrote = %t, err = %v, want the file created", wrote, err)
	}
	folder := filepath.Join(root, "season03-poster.jpg")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if wrote, err := writer.replaceOwnFile(folder, []byte("new"), written); err != nil || wrote {
		t.Errorf("wrote = %t, err = %v, want the directory kept", wrote, err)
	}
}

// A rescan logs each <showid> it skipped, with the reason.
func TestARescanLogsASkippedShowID(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	writeFile(t, filepath.Join(episode.season, seasonNFOName), `<season><showid type="tmdb"></showid></season>`)
	catalog, _ := newSQLiteCatalog(t)
	log := &bytes.Buffer{}
	scan := &scanner{root: episode.root, library: replacedLibrary, kind: libraryKindSeries,
		catalog: catalog, log: log}

	if err := scan.rescan(t.Context(), episode.series); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(log.String(), "skipped the tmdb showid: it names no id") {
		t.Errorf("log = %q, want the skipped showid", log.String())
	}
}
