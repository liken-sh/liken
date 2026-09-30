package main

// What the phases do with a file that replaced another at the same path: the
// probe records the replacement, the trickplay and art phases replace an
// output made for the earlier file and keep one made for this file, and the
// marks phase drops every span of the earlier file.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// replacedAt writes a probe record that matches the new file and records
// the replacement at the time given, as the probe leaves it after it reads
// the new file.
func (e enrichedEpisode) replacedAt(t *testing.T, at time.Time) {
	t.Helper()
	size, modified, err := statFile(e.video)
	if err != nil {
		t.Fatal(err)
	}
	writeFactLedger(t, e.season, factProbe, likenLedger{
		Probes: []probedFile{{Path: replacedEntry, At: at, Modified: modified, Size: size,
			Duration: 1500, Replaced: at,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080}}}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: at, Result: attemptFound}},
	})
}

func TestTheProbeRecordsWhenAFileWasReplaced(t *testing.T) {
	earlier := time.Now().Add(-2 * time.Hour).Truncate(time.Second).UTC()
	cases := []struct {
		name   string
		held   []probedFile
		change bool
		want   time.Time
	}{
		{name: "no earlier record"},
		{name: "an earlier record of the same size",
			held: []probedFile{{Path: replacedEntry, Size: int64(len("the first encode")), Replaced: earlier}},
			want: earlier},
		{name: "an earlier record of another size",
			held:   []probedFile{{Path: replacedEntry, Size: 3345988372, Replaced: earlier}},
			change: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				root := t.TempDir()
				season := filepath.Join(root, replacedSeries, "Season 01")
				video := filepath.Join(season, replacedEntry)
				writeFile(t, video, "the first encode")
				if test.held != nil {
					writeFactLedger(t, season, factProbe, likenLedger{Probes: test.held})
				}
				work, _ := testEnricher(t, libraryKindSeries, root, nil)

				work.probeOne(t.Context(), answeringProbe(ffprobeOfOneFile), filepath.Join(replacedSeries, "Season 01", replacedEntry))

				ledger := artLedger(t, season, factProbe)
				want := test.want
				if test.change {
					want = time.Unix(changeTimeOf(t, video), 0).UTC()
				}
				if len(ledger.Probes) != 1 || !ledger.Probes[0].Replaced.Equal(want) {
					t.Errorf("probes = %+v, want one record replaced at %s", ledger.Probes, want)
				}
			})
		})
	}
}

// Tiles older than the replacement were made from the earlier file. The
// phase decodes the new file and replaces the whole directory.
func TestTheTrickplayPhaseReplacesTilesOfTheEarlierFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		episode := seedEnrichedEpisode(t)
		episode.replace(t)
		episode.replacedAt(t, episode.made.Add(30*time.Minute))
		standInFFmpeg(t, 2)
		work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

		built := work.trickplayOne(t.Context(), trickplayGap{path: episode.path, duration: 1050 * time.Second})

		if !built {
			t.Error("the phase reported no tiles written")
		}
		sheets := namesIn(t, filepath.Join(episode.tiles, trickplayTilesFolder()))
		if len(sheets) != 2 {
			t.Errorf("the tiles folder holds %v, want the two new sheets", sheets)
		}
		if got := readFileString(t, filepath.Join(episode.tiles, trickplayTilesFolder(), "0.jpg")); got == "a sheet of the first encode" {
			t.Error("the sheet of the earlier file is still there")
		}
		for _, name := range namesIn(t, episode.season) {
			if strings.Contains(name, likenTempMark) {
				t.Errorf("the phase left %s on the volume", name)
			}
		}
	})
}

// Tiles made after the replacement are tiles of this file, whoever made
// them, Jellyfin included. The phase records them and decodes nothing.
func TestTheTrickplayPhaseKeepsTilesMadeAfterTheReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		episode := seedEnrichedEpisode(t)
		episode.replace(t)
		episode.replacedAt(t, episode.made.Add(30*time.Minute))
		now := time.Now()
		for _, output := range []string{filepath.Join(episode.tiles, trickplayTilesFolder()), episode.tiles} {
			setModified(t, output, now)
		}
		standInFFmpeg(t, 2)
		work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

		built := work.trickplayOne(t.Context(), trickplayGap{path: episode.path, duration: 1050 * time.Second})

		if built {
			t.Error("the phase decoded a file whose tiles are its own")
		}
		if got := readFileString(t, filepath.Join(episode.tiles, trickplayTilesFolder(), "0.jpg")); got != "a sheet of the first encode" {
			t.Errorf("the sheet reads %q, want the bytes the other tool wrote", got)
		}
		ledger := artLedger(t, episode.season, factTrickplay)
		if item, held := ledger.itemAt(replacedEntry); !held || !item.Provider.is(artProviderExisting) {
			t.Errorf("items = %+v, want the entry that says the tiles were there", ledger.Items)
		}
	})
}

// The episode's still, asked of the fixture's series on TMDb.
func stillLine(t *testing.T) (*artLine, *fakeTMDb) {
	t.Helper()
	endpoint := "/3/tv/2001/season/1/episode/2/images"
	client, fake := newArtTMDb(t, map[string]string{
		tmdbKey(endpoint, "", ""):              imagesAnswer(tmdbStills, "/still.jpg", artLanguage),
		tmdbKey("/t/p/w300/still.jpg", "", ""): testImage,
	})
	return tmdbArtLine(client), fake
}

func TestTheArtPhaseReplacesAThumbnailOfTheEarlierFile(t *testing.T) {
	cases := []struct {
		name string
		made func(episode enrichedEpisode) time.Time
		want string
	}{
		{name: "a still from before the replacement",
			made: func(e enrichedEpisode) time.Time { return e.made }, want: testImage},
		{name: "a still from after the replacement",
			made: func(enrichedEpisode) time.Time { return time.Now() }, want: "a still of the first encode"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				episode := seedEnrichedEpisode(t)
				episode.replace(t)
				episode.replacedAt(t, episode.made.Add(30*time.Minute))
				setModified(t, episode.thumb, test.made(episode))
				line, _ := stillLine(t)
				work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

				work.artOne(t.Context(), line, artTypes[factEpisodeThumb],
					artGap{key: episode.path, tmdb: "2001", season: 1, episode: 2})

				if got := readFileString(t, episode.thumb); got != test.want {
					t.Errorf("the thumbnail holds %q, want %q", got, test.want)
				}
			})
		})
	}
}

// A partial answer keeps the spans of a source that did not answer, because
// its silence says nothing. On a replaced file those spans are the earlier
// file's, so the phase drops them.
func TestTheMarksPhaseDropsEverySpanOfTheEarlierFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		episode := seedEnrichedEpisode(t)
		episode.replace(t)
		episode.replacedAt(t, episode.made.Add(30*time.Minute))
		writeFactLedger(t, episode.season, factMarks, likenLedger{
			Marks: []markEntry{
				{Path: replacedEntry, Kind: markKindIntro, End: milliseconds(50000), Source: providerBlockTheIntroDB},
				{Path: replacedEntry, Kind: markKindCredits, Start: milliseconds(1400000), Source: providerBlockIntroDB},
			},
			Attempts: []likenAttempt{{Path: replacedEntry, At: episode.made, Result: attemptFound}},
		})
		work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)
		line := markLineOf(
			scriptedMarks{block: providerBlockTheIntroDB, entries: []markEntry{
				answeredSpan(providerBlockTheIntroDB, markKindIntro, 0, 61000)}},
			scriptedMarks{block: providerBlockIntroDB, err: errors.New("introdb: 503: busy")},
		)

		work.marksOne(context.Background(), line, episode.path, markFile{episodes: 1})

		ledger := artLedger(t, episode.season, factMarks)
		want := []string{replacedEntry + " intro 0s-1m1s theintrodb"}
		if got := markText(ledger.Marks); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("the ledger holds %v, want %v", got, want)
		}
	})
}

// The two doors that replace an output refuse every path but the one they
// exist for, and keep an output newer than the replacement.
func TestTheReplaceDoorsRefuseAnythingElse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		writer := newVolumeWriter("job")
		before := time.Now().Add(time.Hour).Unix()

		folder := filepath.Join(root, "movie.mkv.d")
		writeFile(t, filepath.Join(folder, "keep.txt"), "a person's file")
		if _, err := writer.replaceEarlierTree(folder, before); err == nil {
			t.Error("the tree door took a directory with no trickplay name")
		}
		poster := filepath.Join(root, "poster.jpg")
		writeFile(t, poster, "a person's poster")
		if _, err := writer.replaceEarlierFile(poster, []byte("new"), before); err == nil {
			t.Error("the file door took a file with no thumbnail name")
		}
		if got := readFileString(t, poster); got != "a person's poster" {
			t.Errorf("the poster holds %q, want the person's bytes", got)
		}

		thumb := filepath.Join(root, "Show - S01E01-thumb.jpg")
		writeFile(t, thumb, "made after the replacement")
		written, err := writer.replaceEarlierFile(thumb, []byte("new"), time.Now().Add(-time.Hour).Unix())
		if err != nil || written {
			t.Errorf("written = %t, err = %v, want the newer file kept", written, err)
		}
		if _, err := os.Stat(folder); err != nil {
			t.Error(err)
		}
	})
}
