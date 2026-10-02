package main

// What the walk does with a video file that a new file replaced at the same
// path: which attempts it lifts, which rows it writes, and what the gap
// counts read after a webhook rescan. The fixture is one episode that every
// file fact has already enriched.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	replacedLibrary = "house/series"
	replacedSeries  = "Quiet Harbor (2008)"
	replacedEntry   = "Quiet Harbor - S01E02.mkv"
	replacedThumb   = "Quiet Harbor - S01E02-thumb.jpg"
	replacedTiles   = "Quiet Harbor - S01E02.trickplay"
)

// One enriched episode on the volume. The facts ran an hour ago, and the
// video was written a day before them.
type enrichedEpisode struct {
	root    string
	series  string
	season  string
	video   string
	path    string
	thumb   string
	tiles   string
	made    time.Time
	written time.Time
}

// writeFactLedger writes one fact's ledger through the types the facts write
// it with.
func writeFactLedger(t *testing.T, folder, fact string, ledger likenLedger) {
	t.Helper()
	data, err := yaml.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, likenDirectory, likenLedgerName(fact)), string(data))
}

// setModified moves one path's modified time, the way a download manager
// copies it from the source file.
func setModified(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// seedEnrichedEpisode writes the episode, its thumbnail, its tile directory,
// and the ledger of every file fact that has read it: probe, arrival,
// trickplay, episode-thumb, and marks.
func seedEnrichedEpisode(t *testing.T) enrichedEpisode {
	t.Helper()
	root := t.TempDir()
	made := time.Now().Add(-time.Hour).Truncate(time.Second).UTC()
	episode := enrichedEpisode{
		root:    root,
		series:  filepath.Join(root, replacedSeries),
		season:  filepath.Join(root, replacedSeries, "Season 01"),
		path:    filepath.Join(replacedSeries, "Season 01", replacedEntry),
		made:    made,
		written: made.Add(-24 * time.Hour),
	}
	episode.video = filepath.Join(episode.season, replacedEntry)
	episode.thumb = filepath.Join(episode.season, replacedThumb)
	episode.tiles = filepath.Join(episode.season, replacedTiles)

	writeFile(t, filepath.Join(episode.series, seriesNFOName),
		`<tvshow><title>Quiet Harbor</title><year>2008</year><uniqueid type="tmdb">2001</uniqueid></tvshow>`)
	writeFile(t, episode.video, "the first encode")
	setModified(t, episode.video, episode.written)
	writeFile(t, episode.thumb, "a still of the first encode")
	writeFile(t, filepath.Join(episode.tiles, trickplayTilesFolder(), "0.jpg"), "a sheet of the first encode")
	for _, output := range []string{episode.thumb, filepath.Join(episode.tiles, trickplayTilesFolder()), episode.tiles} {
		setModified(t, output, made)
	}

	found := []likenAttempt{{Path: replacedEntry, At: made, Result: attemptFound}}
	writeFactLedger(t, episode.season, factProbe, likenLedger{
		Probes: []probedFile{{
			Path: replacedEntry, At: made, Modified: episode.written.Unix(), Size: int64(len("the first encode")),
			Duration: 1500, Bitrate: 8000000,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080},
				{Kind: fileTypeAudio, Codec: "aac", Channels: 2}},
		}},
		Attempts: found,
	})
	writeFactLedger(t, episode.season, factArrival, likenLedger{
		Files:    []arrivalEntry{{Path: replacedEntry, At: episode.written}},
		Attempts: found,
	})
	writeFactLedger(t, episode.season, factTrickplay, likenLedger{
		Items:    []likenItem{{Path: replacedEntry, Provider: providerNames{artProviderExisting}}},
		Attempts: found,
	})
	writeFactLedger(t, episode.season, factEpisodeThumb, likenLedger{
		Items:    []likenItem{{Path: replacedEntry, Provider: providerNames{providerBlockTMDb}}},
		Attempts: found,
	})
	writeFactLedger(t, episode.season, factMarks, likenLedger{
		Marks:    []markEntry{{Path: replacedEntry, Kind: markKindIntro, End: milliseconds(50000), Source: providerBlockTheIntroDB}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: made, Result: attemptFound, Provider: providerNames{providerBlockTheIntroDB}}},
	})
	return episode
}

// replace writes a new encode under the old name and gives it the old
// modified time, which is what a download manager does on an import.
func (e enrichedEpisode) replace(t *testing.T) {
	t.Helper()
	writeFile(t, e.video, "the second encode, a little larger")
	setModified(t, e.video, e.written)
}

func (e enrichedEpisode) scan() folderScan {
	return folderScan{root: e.root, library: replacedLibrary, kind: libraryKindSeries}
}

// liftedFacts names the facts whose attempt at one path the read lifted.
func liftedFacts(result *walkResult, path string) []string {
	var facts []string
	for _, attempt := range result.attempts {
		if attempt.Item == path {
			facts = append(facts, attempt.Fact)
		}
	}
	slices.Sort(facts)
	return facts
}

// linkedTo reports whether a file row links to any item.
func linkedTo(result *walkResult, path string) []string {
	for _, row := range result.files {
		if row.Path == path {
			return row.Items
		}
	}
	return nil
}

func TestAFileReplacedWithANewSizeReopensEveryFileFact(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.replace(t)

	result := readFolder(episode.scan(), episode.series)

	if got := liftedFacts(result, episode.path); !slices.Equal(got, []string{factArrival}) {
		t.Errorf("lifted %v, want the arrival alone", got)
	}
	row := fileRowAt(t, result, episode.path)
	if row.Probed != 0 || row.DurationMs != 0 || row.VideoCodec != "" {
		t.Errorf("row = %+v, want no column from the earlier file's probe", row)
	}
	if row.Trickplay != "" {
		t.Errorf("trickplay = %q, want none from tiles of the earlier file", row.Trickplay)
	}
	if row.Arrived != episode.written.Unix() {
		t.Errorf("arrived = %d, want the first arrival %d", row.Arrived, episode.written.Unix())
	}
	if rows := streamRowsAt(result, episode.path); rows != nil {
		t.Errorf("streams = %+v, want none of the earlier file", rows)
	}
	for _, mark := range result.marks {
		if mark.Path == episode.path {
			t.Errorf("mark = %+v, want no span of the earlier file", mark)
		}
	}
	if items := linkedTo(result, filepath.Join(replacedSeries, "Season 01", replacedThumb)); items != nil {
		t.Errorf("the thumbnail links to %v, want no episode for a still of the earlier file", items)
	}
}

// gapsOf reads the four file facts' gap counts of the fixture's library now.
func gapsOf(t *testing.T, catalog *Catalog) map[string]int {
	t.Helper()
	counts, err := catalog.gapCounts(t.Context(), replacedLibrary, time.Now().UTC(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]int{
		factProbe: counts[factProbe], factTrickplay: counts[factTrickplay],
		factEpisodeThumb: counts[factEpisodeThumb], factMarks: counts[factMarks],
	}
}

// rescan reads the series folder into the catalog the way a webhook does:
// the upsert, then the sweep of the rows the read did not produce.
func (e enrichedEpisode) rescan(t *testing.T, catalog *Catalog) {
	t.Helper()
	if _, _, _, err := rescanFolder(t.Context(), catalog, e.scan(), e.series); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnchangedFileStaysClosed(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)

	episode.rescan(t, catalog)

	want := map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 0, factMarks: 0}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps = %v, want %v", got, want)
	}
	result := readFolder(episode.scan(), episode.series)
	wantFacts := []string{factArrival, factEpisodeThumb, factMarks, factProbe, factTrickplay}
	if got := liftedFacts(result, episode.path); !slices.Equal(got, wantFacts) {
		t.Errorf("lifted %v, want %v", got, wantFacts)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// A new modified time on a file of the same size is the probe's work alone.
// Its found attempt no longer holds the probe gap closed, and no other fact
// opens.
func TestANewModifiedTimeReopensTheProbeAlone(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)
	episode.rescan(t, catalog)

	setModified(t, episode.video, episode.written.Add(time.Minute))
	episode.rescan(t, catalog)

	want := map[string]int{factProbe: 1, factTrickplay: 0, factEpisodeThumb: 0, factMarks: 0}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps = %v, want %v", got, want)
	}
}

// Once the probe has read the new file, its record matches the file again,
// and the time it records for the replacement is what tells an attempt on
// the earlier file from one on this file.
func TestAnAttemptBeforeTheReplacementDoesNotCount(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.replace(t)
	size, modified, err := statFile(episode.video)
	if err != nil {
		t.Fatal(err)
	}
	replaced := episode.made.Add(30 * time.Minute)
	later := replaced.Add(10 * time.Minute)
	writeFactLedger(t, episode.season, factProbe, likenLedger{
		Probes: []probedFile{{Path: replacedEntry, At: replaced, Modified: modified, Size: size,
			Duration: 1500, Replaced: replaced,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080}}}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: replaced, Result: attemptFound}},
	})
	writeFactLedger(t, episode.season, factTrickplay, likenLedger{
		Attempts: []likenAttempt{{Path: replacedEntry, At: later, Result: attemptFound}},
	})
	for _, output := range []string{filepath.Join(episode.tiles, trickplayTilesFolder()), episode.tiles} {
		setModified(t, output, later)
	}

	result := readFolder(episode.scan(), episode.series)

	want := []string{factArrival, factProbe, factTrickplay}
	if got := liftedFacts(result, episode.path); !slices.Equal(got, want) {
		t.Errorf("lifted %v, want %v: the marks attempt and the thumbnail attempt are older than the replacement", got, want)
	}
	for _, mark := range result.marks {
		if mark.Path == episode.path {
			t.Errorf("mark = %+v, want no span of the earlier file", mark)
		}
	}
	if row := fileRowAt(t, result, episode.path); row.Trickplay == "" {
		t.Error("the tiles made after the replacement read as none")
	}
}

// The whole flow a webhook drives. The rescan after the import opens the
// probe and the thumbnail. Once the probe has measured the new file, the
// next rescan opens the tiles and the marks, which wait for its length.
func TestAWebhookRescanOpensTheFactsOfAReplacedFile(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	catalog, _ := newSQLiteCatalog(t)
	episode.rescan(t, catalog)

	episode.replace(t)
	if err := os.Remove(episode.thumb); err != nil {
		t.Fatal(err)
	}
	episode.rescan(t, catalog)

	want := map[string]int{factProbe: 1, factTrickplay: 0, factEpisodeThumb: 1, factMarks: 0}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps after the import = %v, want %v", got, want)
	}

	work, _ := testEnricher(t, libraryKindSeries, episode.root, catalog)
	work.library = replacedLibrary
	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}
	episode.rescan(t, catalog)

	want = map[string]int{factProbe: 0, factTrickplay: 1, factEpisodeThumb: 1, factMarks: 1}
	if got := gapsOf(t, catalog); !mapsEqual(got, want) {
		t.Errorf("gaps after the probe = %v, want %v", got, want)
	}
}

// A person reads why a gap count grew in the scan log: one line per
// replaced file with both sizes, and one line of counts.
func TestARescanLogsTheFileItFoundReplaced(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.replace(t)
	catalog, _ := newSQLiteCatalog(t)
	log := &bytes.Buffer{}
	scan := &scanner{root: episode.root, library: replacedLibrary, kind: libraryKindSeries,
		catalog: catalog, log: log}

	if err := scan.rescan(t.Context(), episode.series); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"bytes in the probe record, 34 on the volume",
		"16 bytes in the probe record",
		"reopened the facts of 1 replaced files and 0 missing outputs",
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log = %q, want %q", log.String(), want)
		}
	}
}
