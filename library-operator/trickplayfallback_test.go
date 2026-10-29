package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The decode on the render node, and the software decode that runs when the
// render node refuses a file.

// A stand-in for ffmpeg whose decode on a render node fails after it wrote the
// given number of sheets, the way a GPU with no decoder for the file's codec
// fails. The software decode writes softwareSheets, or fails where that is
// below zero. Each call appends its arguments to the returned file, one line
// per call.
func standInFFmpegRefusingTheRenderNode(t *testing.T, hardwareSheets, softwareSheets int) string {
	t.Helper()
	dir := t.TempDir()
	seed := filepath.Join(dir, "sheet.jpg")
	seedSheet(t, seed)
	calls := filepath.Join(dir, "calls")
	sheets := func(count int) string {
		return "i=0\nwhile [ $i -lt " + strconv.Itoa(count) +
			" ]; do cp \"" + seed + "\" \"$out/$i.jpg\"; i=$((i + 1)); done\n"
	}
	software := sheets(softwareSheets)
	if softwareSheets < 0 {
		software = "echo 'Invalid data found when processing input' >&2\nexit 1\n"
	}
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\nfor last; do :; done\nout=$(dirname \"$last\")\n" +
		"case \"$*\" in *-hwaccel*)\n" + sheets(hardwareSheets) +
		"echo 'Your platform does not support hardware accelerated AV1 decoding.' >&2\nexit 1;;\nesac\n" +
		software
	writeFile(t, filepath.Join(dir, "ffmpeg"), script)
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// The arguments of each call the stand-in above recorded.
func ffmpegCalls(t *testing.T, calls string) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(readFileString(t, calls)), "\n")
}

// A file the render node refuses is tiled in software. The sheets the failed
// decode wrote before it stopped do not survive, because they cover only the
// start of the file and the software decode numbers its sheets from zero.
func TestARefusedDecodeOnTheRenderNodeRunsAgainInSoftware(t *testing.T) {
	root := t.TempDir()
	item := seedTrickplayItem(t, root, 1050*time.Second)
	seedRenderNodes(t, "renderD128")
	calls := standInFFmpegRefusingTheRenderNode(t, 3, 2)
	work, log := testFactWorker(t, trickplayWorker, libraryKindMovies, root)

	work.trickplayOne(t.Context(), item)

	if left := namesIn(t, trickplayTilesUnder(root)); !slices.Equal(left, []string{"0.jpg", "1.jpg"}) {
		t.Errorf("the tiles folder holds %v, want the software decode's sheets alone", left)
	}
	made := ffmpegCalls(t, calls)
	if len(made) != 2 || !strings.Contains(made[0], "-hwaccel vaapi") || strings.Contains(made[1], "-hwaccel") {
		t.Errorf("ffmpeg ran %q, want the render node and then software", made)
	}
	if !strings.Contains(log.String(), "runs again with a software decode") ||
		!strings.Contains(log.String(), "hardware accelerated AV1 decoding") {
		t.Errorf("log = %q, want the retry and ffmpeg's own words", log)
	}
	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the file recorded as found", ledger.Attempts)
	}
}

// A file that neither decoder reads is the file's own state, and a miss.
func TestAFileNeitherDecoderReadsIsAMiss(t *testing.T) {
	root := t.TempDir()
	item := seedTrickplayItem(t, root, 100*time.Second)
	seedRenderNodes(t, "renderD128")
	calls := standInFFmpegRefusingTheRenderNode(t, 0, -1)
	work, _ := testFactWorker(t, trickplayWorker, libraryKindMovies, root)

	work.trickplayOne(t.Context(), item)

	if made := ffmpegCalls(t, calls); len(made) != 2 {
		t.Errorf("ffmpeg ran %q, want the render node and then software", made)
	}
	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("attempts = %+v, want the file recorded as a miss", ledger.Attempts)
	}
}

// A run that was stopped while the render node decoded does not start a
// software decode, and the stop is an error and not a fact about the file.
func TestAStoppedDecodeOnTheRenderNodeDoesNotRunAgain(t *testing.T) {
	root := t.TempDir()
	item := seedTrickplayItem(t, root, 100*time.Second)
	seedRenderNodes(t, "renderD128")
	calls := standInFFmpegRefusingTheRenderNode(t, 0, 2)
	work, _ := testFactWorker(t, trickplayWorker, libraryKindMovies, root)
	stopped, stop := context.WithCancel(t.Context())
	stop()

	work.trickplayOne(stopped, item)

	if fileExistsInTest(t, calls) {
		t.Errorf("ffmpeg ran %q after the stop, want no decode", ffmpegCalls(t, calls))
	}
	ledger, err := readLikenLedger(filepath.Join(root, trickplayFolder), factTrickplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want the stop recorded as an error", ledger.Attempts)
	}
}

// The decode on the render node stops at its first decode error, so a codec
// the GPU does not decode costs one error line and not one line per frame.
// The software decode carries on past a damaged frame, the way a player does.
func TestOnlyTheDecodeOnTheRenderNodeStopsAtItsFirstError(t *testing.T) {
	cases := []struct {
		name   string
		nodes  []string
		xerror bool
	}{
		{name: "on the render node", nodes: []string{"renderD128"}, xerror: true},
		{name: "in software"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := standInFFmpegRecordingArguments(t)
			devices := seedRenderNodes(t, test.nodes...)

			if err := ffmpegSheets(t.Context(), "One (2001).mkv", t.TempDir(), renderNode()); err != nil {
				t.Fatal(err)
			}

			got := strings.Contains(readFileString(t, record), " -xerror ")
			if got != test.xerror {
				t.Errorf("-xerror on the command line is %v with %v in %s, want %v",
					got, test.nodes, devices, test.xerror)
			}
		})
	}
}
