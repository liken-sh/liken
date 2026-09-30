package main

// A found attempt says its output is on the volume. These tests remove the
// output and read what the walk lifts and what the gaps count.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestADeletedThumbnailReopensTheEpisodeThumbGap(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)
	episode.rescan(t, catalog)

	if err := os.Remove(episode.thumb); err != nil {
		t.Fatal(err)
	}
	episode.rescan(t, catalog)

	want := map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 1, factMarks: 0}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}

func TestADeletedTileDirectoryReopensTheTrickplayGap(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)
	episode.rescan(t, catalog)

	if err := os.RemoveAll(episode.tiles); err != nil {
		t.Fatal(err)
	}
	episode.rescan(t, catalog)

	want := map[string]int{factProbe: 0, factTrickplay: 1, factEpisodeThumb: 0, factMarks: 0}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}

// A miss promises no output, so an attempt that found nothing is lifted with
// no output beside it, and its window holds as it always has.
func TestAMissWithNoOutputStaysLifted(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	if err := os.Remove(episode.thumb); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(episode.tiles); err != nil {
		t.Fatal(err)
	}
	missed := []likenAttempt{{Path: replacedEntry, At: episode.made, Result: attemptNothing}}
	writeFactLedger(t, episode.season, factTrickplay, likenLedger{Attempts: missed})
	writeFactLedger(t, episode.season, factEpisodeThumb, likenLedger{Attempts: missed})

	result := readFolder(episode.scan(), episode.series)

	want := []string{factArrival, factEpisodeThumb, factMarks, factProbe, factTrickplay}
	if got := liftedFacts(result, episode.path); !slices.Equal(got, want) {
		t.Errorf("lifted %v, want %v", got, want)
	}
}

// Every other fact that writes a file keeps its found attempt only while
// the walk finds that file: the title's art, a season's art, and the pulled
// trailer.
func TestAFoundAttemptWhoseFileIsGoneIsNotLifted(t *testing.T) {
	cases := []struct {
		name  string
		fact  string
		entry string
		file  string
	}{
		{name: "a poster", fact: factPoster, entry: "poster.jpg", file: "poster.jpg"},
		{name: "a season poster", fact: factSeasonPoster, entry: "season01-poster.jpg", file: "season01-poster.jpg"},
		{name: "a trailer file", fact: factTrailerFile, entry: likenSelfPath,
			file: filepath.Join("trailers", "Quiet Harbor Trailer.mp4")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			writeFactLedger(t, episode.series, test.fact, likenLedger{
				Attempts: []likenAttempt{{Path: test.entry, At: episode.made, Result: attemptFound}},
			})
			present := readFolder(episode.scan(), episode.series)
			writeFile(t, filepath.Join(episode.series, test.file), "the output")
			held := readFolder(episode.scan(), episode.series)

			if got := factsLifted(present, test.fact); got != 0 {
				t.Errorf("with the file gone the read lifted %d attempts, want none", got)
			}
			if got := factsLifted(held, test.fact); got != 1 {
				t.Errorf("with the file there the read lifted %d attempts, want one", got)
			}
		})
	}
}

// factsLifted counts the attempts of one fact a read lifted.
func factsLifted(result *walkResult, fact string) int {
	count := 0
	for _, attempt := range result.attempts {
		if attempt.Fact == fact {
			count++
		}
	}
	return count
}

// A person's headshot and biography are files the contributors phase
// writes, so their found attempts need the files as well.
func TestAContributorFileThatIsGoneReopensItsFact(t *testing.T) {
	cases := []struct {
		fact string
		file string
	}{
		{fact: factContributorHeadshot, file: contributorHeadshotName},
		{fact: factContributorBiography, file: contributorBiographyName},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, contributorsDirectory, "ma", "mara-quill")
			writeFile(t, filepath.Join(entry, contributorFileName), "name: Mara Quill\n")
			writeFactLedger(t, entry, test.fact, likenLedger{
				Attempts: []likenAttempt{{Path: likenSelfPath, At: time.Now().UTC(), Result: attemptFound}},
			})
			gone := &walkResult{}
			readContributorFolder(root, replacedLibrary, entry, gone)
			writeFile(t, filepath.Join(entry, test.file), "the output")
			held := &walkResult{}
			readContributorFolder(root, replacedLibrary, entry, held)

			if got := factsLifted(gone, test.fact); got != 0 {
				t.Errorf("with the file gone the read lifted %d attempts, want none", got)
			}
			if got := factsLifted(held, test.fact); got != 1 {
				t.Errorf("with the file there the read lifted %d attempts, want one", got)
			}
		})
	}
}
