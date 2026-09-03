package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// what these tests read: what the trickplay fact does when the catalog, the
// volume, or ffmpeg's own output will not answer, and what the staging door
// refuses.

// A gap read that fails ends the container, because the list is the work.
func TestTheTrickplayFactFailsWhereTheGapReadIsRefused(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := work.trickplayFact(t.Context()); err == nil {
		t.Error("the fact ran, want the refused read to end the container")
	}
}

// A row the gap read cannot use is no gap, so a short answer or a row with no
// path leaves the list rather than the container.
func TestARowTheTrickplayGapCannotUseIsNoGap(t *testing.T) {
	body := `{"columns":["path","duration_ms"]}` + "\n" +
		`{"row":[1,["",100]]}` + "\n" +
		`{"row":[2,["A/a.mkv"]]}` + "\n" +
		`{"eoq":{"time":0.1}}` + "\n"

	gaps, err := streamingServer(t, http.StatusOK, body).
		trickplayGaps(t.Context(), trickplayLibrary, ledgerTime)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want neither row", gaps)
	}
}

// A sheet ffmpeg wrote that is no image, and a directory the volume will not
// take, are both errors with a date, so the file is tried again on a later
// run and never opened twice in one.
func TestTheTrickplayFactRecordsWhatTheVolumeRefused(t *testing.T) {
	cases := []struct {
		name  string
		setUp func(t *testing.T, root string)
	}{
		{name: "a sheet that is no image", setUp: func(t *testing.T, root string) {
			t.Helper()
			standInFFmpegWritingJunk(t)
		}},
		{name: "a tiles folder that takes no map", setUp: func(t *testing.T, root string) {
			t.Helper()
			video := filepath.Join(root, trickplayFolder, trickplayFile)
			staging := newVolumeWriter("movies-enrich").temporary(trickplayDirectory(video))
			standInFFmpegSealing(t, filepath.Join(staging, trickplayTilesFolder()))
		}},
		{name: "a crash between two sheets", setUp: func(t *testing.T, root string) {
			t.Helper()
			standInFFmpegFailingAfterASheet(t)
		}},
		{name: "a staging directory ffmpeg took with it", setUp: func(t *testing.T, root string) {
			t.Helper()
			standInFFmpegTakingItsOutput(t)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedTrickplayGap(t, catalog, root, 100*time.Second)
			test.setUp(t, root)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)

			if err := work.trickplayFact(t.Context()); err != nil {
				t.Fatal(err)
			}

			ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
				t.Errorf("attempts = %+v, want the one file recorded as an error", ledger.Attempts)
			}
		})
	}
}

// A staging directory the volume cannot hold is an error, and ffmpeg never
// runs, because there is nowhere for it to write.
func TestAStagingDirectoryTheVolumeCannotHoldIsAnError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "One (2001)"), "a file where the folder goes")

	if _, err := newVolumeWriter("movies-enrich").
		stageTree(filepath.Join(root, "One (2001)", "One (2001).trickplay")); err == nil {
		t.Error("the staging directory was made, want an error")
	}
}

// A folder that takes no rename leaves the tree staged and the target absent,
// and the log names the write it could not make. The ledger is in that same
// folder, so the run records nothing there either.
func TestAFolderThatTakesNoRenameLandsNothing(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	standInFFmpegSealing(t, filepath.Join(root, trickplayFolder))
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(log.String(), "could not write") {
		t.Errorf("log = %q, want the line that names the write it could not make", log)
	}
	video := filepath.Join(root, trickplayFolder, trickplayFile)
	if fileExistsInTest(t, trickplayDirectory(video)) {
		t.Error("the failed rename left the tiles under the title's own name")
	}
}

// A folder that takes no staging directory stops the file before ffmpeg runs,
// and the log names the step that could not be made. The ledger is in that
// same folder, so the run records nothing there either.
func TestAFolderThatTakesNoStagingDirectoryStopsTheFile(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	standInFFmpeg(t, 1)
	folder := filepath.Join(root, trickplayFolder)
	if err := os.Chmod(folder, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o755) })
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(log.String(), "could not stage the trickplay of") {
		t.Errorf("log = %q, want the line that names the staging it could not make", log)
	}
}

// A crash between two sheets leaves nothing under the title's own name: the
// tree lands with one rename or not at all, and the sheets that were written
// go with the staging directory.
func TestACrashBetweenTwoSheetsLandsNoTrickplayDirectory(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrickplayGap(t, catalog, root, 100*time.Second)
	standInFFmpegFailingAfterASheet(t)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.trickplayFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, trickplayFolder))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), trickplayExtension) {
			t.Errorf("the crashed run left %s on the volume", entry.Name())
		}
	}
}

// The file a crashed run left behind is tiled again after the error window
// and not on the next run. A file ffmpeg refused is a miss with a date and
// waits the long window.
func TestEveryAttemptKindGatesTheTrickplayGapAgainstTheRealSchema(t *testing.T) {
	for _, test := range attemptWindowCases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seed := &walkResult{
				files: []fileRow{{Path: "A/a.mkv", Library: trickplayLibrary, Present: true,
					Type: fileTypeVideo, DurationMs: 6540000, VideoCodec: "h264"}},
				attempts: []attemptRow{{Library: trickplayLibrary, Item: "A/a.mkv",
					Fact: factTrickplay, At: ledgerTime.Add(-test.age).Unix(), Result: test.result}},
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			gaps, err := catalog.gapCounts(t.Context(), trickplayLibrary, ledgerTime)
			if err != nil {
				t.Fatal(err)
			}
			if gaps[factTrickplay] != test.wantGap {
				t.Errorf("trickplay gap = %d, want %d", gaps[factTrickplay], test.wantGap)
			}
		})
	}
}

// A file the probe found no video stream in has nothing to tile, so it is no
// gap, whatever length its container states.
func TestAFileWithNoVideoStreamIsNoTrickplayGap(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{
		files: []fileRow{{Path: "A/a.mkv", Library: trickplayLibrary, Present: true,
			Type: fileTypeVideo, DurationMs: 200585000}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	gaps, err := catalog.gapCounts(t.Context(), trickplayLibrary, ledgerTime)
	if err != nil {
		t.Fatal(err)
	}
	if gaps[factTrickplay] != 0 {
		t.Errorf("trickplay gap = %d, want none for a file with no video stream", gaps[factTrickplay])
	}
}
