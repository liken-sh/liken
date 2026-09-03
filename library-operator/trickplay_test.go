package main

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// what these tests read: the gap the trickplay fact works from, the sheets and
// the map it leaves beside a video, what it does with a directory another tool
// wrote, and what each outcome records in the ledger.

// The library, the folder, and the file every case below works on.
const (
	trickplayLibrary = "house/movies"
	trickplayFolder  = "One (2001)"
	trickplayFile    = "One (2001).mkv"
)

// One sheet the stand-in copies: a real JPEG of a whole grid, so the tile size
// reads back off its own header. Ten by ten thumbnails of 32 by 18 pixels.
func seedSheet(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	grid := image.NewGray(image.Rect(0, 0, 32*trickplayColumns, 18*trickplayRows))
	if err := jpeg.Encode(file, grid, nil); err != nil {
		t.Fatal(err)
	}
}

// A stand-in for ffmpeg on PATH. It reads the output pattern out of its last
// argument, the way the real one does, and writes the number of numbered
// sheets the case asks for into that directory. A count below zero is the
// tool that fails.
func standInFFmpeg(t *testing.T, sheets int) {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "sheet.jpg")
	seedSheet(t, seed)

	script := "#!/bin/sh\nfor last; do :; done\n"
	if sheets < 0 {
		script += "echo 'Invalid data found when processing input' >&2\nexit 1\n"
	} else {
		script += "out=$(dirname \"$last\")\ni=0\nwhile [ $i -lt " + strconv.Itoa(sheets) +
			" ]; do cp \"" + seed + "\" \"$out/$i.jpg\"; i=$((i + 1)); done\n"
	}
	writeFile(t, filepath.Join(dir, "ffmpeg"), script)
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stand-in leads the path, so it answers in place of any ffmpeg the
	// machine holds, and the script still reaches the tools it copies with.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A stand-in for ffmpeg that writes a sheet no image reader can measure, so a
// test drives what the fact does with output it cannot use.
func standInFFmpegWritingJunk(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nfor last; do :; done\necho junk > \"$(dirname \"$last\")/0.jpg\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A stand-in for ffmpeg that writes one sheet and then fails, which is the
// crash between two sheets: some of the tree is on the volume and the run
// never reaches the rename.
func standInFFmpegFailingAfterASheet(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "sheet.jpg")
	seedSheet(t, seed)
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nfor last; do :; done\ncp \""+seed+"\" \"$(dirname \"$last\")/0.jpg\"\nkill -KILL $$\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A stand-in for ffmpeg that writes a sheet and then seals a directory
// against the writes that follow it, so a test drives the map and the rename
// that come after the sheets are on the volume.
func standInFFmpegSealing(t *testing.T, sealed string) {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "sheet.jpg")
	seedSheet(t, seed)
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nfor last; do :; done\nout=$(dirname \"$last\")\n"+
			"cp \""+seed+"\" \"$out/0.jpg\"\nchmod 555 \""+sealed+"\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A stand-in for ffmpeg that takes its own output directory with it, so a
// test drives the read of a staging directory that is not there.
func standInFFmpegTakingItsOutput(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nfor last; do :; done\nrmdir \"$(dirname \"$last\")\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// seedTrickplayGap seeds one movie with one video file that carries a length
// and no trickplay directory, which is the shape of a trickplay gap.
func seedTrickplayGap(t *testing.T, catalog *Catalog, root string, duration time.Duration) {
	t.Helper()
	writeFile(t, filepath.Join(root, trickplayFolder, trickplayFile), "video")
	seed := &walkResult{
		movies: []movieRow{{Id: "movie:path:x", Library: trickplayLibrary, Kind: libraryKindMovies,
			Path: trickplayFolder, Title: trickplayFolder}},
		files: []fileRow{{Path: filepath.Join(trickplayFolder, trickplayFile), Library: trickplayLibrary,
			Present: true, Type: fileTypeVideo, DurationMs: duration.Milliseconds(), VideoCodec: "h264",
			Items: []string{"movie:path:x"}}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The folder the sheets and the map land in for the file above.
func trickplayTilesUnder(root string) string {
	return filepath.Join(root, trickplayFolder,
		strings.TrimSuffix(trickplayFile, ".mkv")+trickplayExtension, trickplayTilesFolder())
}

// A video with a length and no tiles is the gap. A video the probe has not
// reached, one that already carries tiles, and a file that is not a video are
// none of them.
func TestTheTrickplayGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{files: []fileRow{
		{Path: "A/a.mkv", Library: trickplayLibrary, Present: true, Type: fileTypeVideo, DurationMs: 6540000, VideoCodec: "h264"},
		{Path: "B/b.mkv", Library: trickplayLibrary, Present: true, Type: fileTypeVideo},
		{Path: "C/c.mkv", Library: trickplayLibrary, Present: true, Type: fileTypeVideo, DurationMs: 100, VideoCodec: "h264",
			Trickplay: "C/c.trickplay"},
		{Path: "D/d.srt", Library: trickplayLibrary, Present: true, Type: fileTypeSubtitle, DurationMs: 100},
	}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.trickplayGaps(t.Context(), trickplayLibrary, ledgerTime)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].path != "A/a.mkv" {
		t.Fatalf("gaps = %+v, want the one video with a length and no tiles", gaps)
	}
	if gaps[0].duration != 6540*time.Second {
		t.Errorf("duration = %s, want the length the probe wrote", gaps[0].duration)
	}
}

// An attempt inside the retry window takes the file out of the gap, and the
// count the reporter reads is the same one the container works from.
func TestATrickplayAttemptClosesItsOwnGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{
		files: []fileRow{{Path: "A/a.mkv", Library: trickplayLibrary, Present: true,
			Type: fileTypeVideo, DurationMs: 6540000, VideoCodec: "h264"}},
		attempts: []attemptRow{{Library: trickplayLibrary, Item: "A/a.mkv", Fact: factTrickplay,
			At: ledgerTime.Unix(), Result: attemptFound}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "inside the retry window", now: ledgerTime.Add(time.Hour), want: 0},
		{name: "past the retry window", now: ledgerTime.Add(2 * defaultRetryInterval), want: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			gaps, err := catalog.gapCounts(t.Context(), trickplayLibrary, test.now)
			if err != nil {
				t.Fatal(err)
			}
			if gaps[factTrickplay] != test.want {
				t.Errorf("trickplay gap = %d, want %d", gaps[factTrickplay], test.want)
			}
		})
	}
}

// The whole run over one title: the sheets under Jellyfin's own folder name,
// the map beside them, and no staging directory left on the volume.
func TestTheTrickplayFactWritesSheetsAndAMapBesideTheVideo(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 1050*time.Second)
	standInFFmpeg(t, 2)
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	tiles := trickplayTilesUnder(root)
	for _, name := range []string{"0.jpg", "1.jpg", trickplayIndexName} {
		if !fileExistsInTest(t, filepath.Join(tiles, name)) {
			t.Errorf("%s is not beside the video", name)
		}
	}
	if !strings.Contains(log.String(), "wrote the trickplay of 1 of the 1 files") {
		t.Errorf("log = %q, want the count of the files it filled", log)
	}
	entries, err := os.ReadDir(filepath.Join(root, trickplayFolder))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), likenTempMark) {
			t.Errorf("the run left %s on the volume", entry.Name())
		}
	}
}

// The map covers the thumbnails of the last, partly filled sheet, and it stops
// at the title's own end rather than at the end of the padded grid.
func TestTheMapCoversTheLastPartialSheet(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 1050*time.Second)
	standInFFmpeg(t, 2)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	index := readFileString(t, filepath.Join(trickplayTilesUnder(root), trickplayIndexName))
	if got := strings.Count(index, "#xywh="); got != 105 {
		t.Errorf("cues = %d, want one per ten seconds of the title", got)
	}
	if !strings.Contains(index, "\n1.jpg#xywh=128,0,32,18\n") {
		t.Error("the map names no thumbnail of the second sheet")
	}
	if !strings.Contains(index, "00:17:20.000 --> 00:17:30.000\n") {
		t.Error("the last cue does not end where the title ends")
	}
}

// A directory another tool wrote is never opened. The fact records that the
// tiles are there and leaves every byte of them.
func TestTheTrickplayFactLeavesTilesAnotherToolWrote(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 1050*time.Second)
	held := filepath.Join(trickplayTilesUnder(root), "0.jpg")
	writeFile(t, held, "the sheet another tool wrote")
	standInFFmpeg(t, 2)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, held); got != "the sheet another tool wrote" {
		t.Errorf("the sheet reads %q, want the bytes the other tool left", got)
	}
	if fileExistsInTest(t, filepath.Join(trickplayTilesUnder(root), trickplayIndexName)) {
		t.Error("the fact wrote a map into a directory another tool holds")
	}
	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(artProviderExisting) {
		t.Errorf("items = %+v, want the tiles recorded as already there", ledger.Items)
	}
}

// What one file's outcome records. A miss and an error are both attempts with
// a date, so a file that will not decode is not opened again every run.
func TestWhatOneTrickplayAttemptRecords(t *testing.T) {
	cases := []struct {
		name   string
		sheets int
		want   string
	}{
		{name: "the sheets landed", sheets: 2, want: attemptFound},
		{name: "ffmpeg read no frame", sheets: 0, want: attemptNothing},
		{name: "ffmpeg could not read the file", sheets: -1, want: attemptNothing},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedTrickplayGap(t, catalog, root, 100*time.Second)
			standInFFmpeg(t, test.sheets)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)

			if err := work.trickplayFact(t.Context()); err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Path != trickplayFile {
				t.Fatalf("attempts = %+v, want the one file's own", ledger.Attempts)
			}
			if ledger.Attempts[0].Result != test.want {
				t.Errorf("result = %q, want %q", ledger.Attempts[0].Result, test.want)
			}
		})
	}
}

// The tiles a walk already found close the gap without a decode, and the
// ledger says the file has them.
func TestATrickplayDirectoryOnTheVolumeCostsNoDecode(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	writeFile(t, filepath.Join(trickplayTilesUnder(root), "0.jpg"), "sheet")
	standInFFmpeg(t, -1)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the one file recorded as filled", ledger.Attempts)
	}
}

// A narrowed Job works its own folder alone, the rule every fact holds.
func TestTheTrickplayFactWorksOnlyItsOwnScope(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	standInFFmpeg(t, 1)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Another Folder"

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if fileExistsInTest(t, filepath.Join(trickplayTilesUnder(root), "0.jpg")) {
		t.Error("the fact wrote tiles for a folder outside its scope")
	}
}

// The staging directory a crashed run left behind is cleared before ffmpeg
// writes, so no sheet of that run becomes a tile of this one.
func TestAStrayStagingDirectoryDoesNotBecomeTiles(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	video := filepath.Join(root, trickplayFolder, trickplayFile)
	staging := newVolumeWriter("movies-enrich").temporary(trickplayDirectory(video))
	writeFile(t, filepath.Join(staging, trickplayTilesFolder(), "9.jpg"), "a sheet of the run that failed")
	standInFFmpeg(t, 1)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	tiles := trickplayTilesUnder(root)
	if fileExistsInTest(t, filepath.Join(tiles, "9.jpg")) {
		t.Error("a sheet of the failed run became a tile")
	}
	if !fileExistsInTest(t, filepath.Join(tiles, "0.jpg")) {
		t.Error("this run's own sheet is not beside the video")
	}
}

// The trickplay container stands only where the Library turns the fact on,
// and it names one fact, a memory line, and a CPU request of its own.
func TestTheTrickplayContainerStandsWhereTheLibraryTurnsItOn(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
	}{
		{name: "a library that turns it on", enabled: true},
		{name: "a library that leaves it off"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Trickplay.Enabled = test.enabled
			job := testEnrichJob(library, "", readyProvider("tmdb", "house"))

			var tiles Container
			for _, container := range job.Spec.Template.Spec.InitContainers {
				if container.Name == trickplayContainerName {
					tiles = container
				}
			}
			if (tiles.Name != "") != test.enabled {
				t.Fatalf("the pod holds the trickplay container: %t, want %t",
					tiles.Name != "", test.enabled)
			}
			if !test.enabled {
				return
			}
			facts := ""
			for _, variable := range tiles.Env {
				if variable.Name == libraryFactsVariable {
					facts = variable.Value
				}
			}
			if facts != factTrickplay {
				t.Errorf("%s = %q, want %q", libraryFactsVariable, facts, factTrickplay)
			}
			if tiles.Resources.Limits["memory"] != trickplayMemoryLimit ||
				trickplayMemoryLimit == scannerMemoryLimit {
				t.Errorf("memory limit = %q, want %q, above the scanner's %q",
					tiles.Resources.Limits["memory"], trickplayMemoryLimit, scannerMemoryLimit)
			}
			if tiles.Resources.Requests["cpu"] != trickplayCPURequest ||
				trickplayCPURequest == scannerCPURequest {
				t.Errorf("cpu request = %q, want %q, above the scanner's %q",
					tiles.Resources.Requests["cpu"], trickplayCPURequest, scannerCPURequest)
			}
		})
	}
}

// The trickplay gap opens a Job only where the Library turns the fact on. No
// provider serves that fact, so the check on the sources cannot answer for it,
// and a library that leaves it off would otherwise schedule a Job for ever.
func TestTheTrickplayGapOpensAJobOnlyWhereTheLibraryTurnsItOn(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
	}{
		{name: "a library that turns it on", enabled: true},
		{name: "a library that leaves it off"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library, providers := libraryWithProvider()
			library.Spec.Trickplay.Enabled = test.enabled
			report := &libraryReport{Gaps: map[string]int{factTrickplay: 3}}

			if got := gapOpen(library, report, providers); got != test.enabled {
				t.Errorf("the gap is open: %t, want %t", got, test.enabled)
			}
		})
	}
}
