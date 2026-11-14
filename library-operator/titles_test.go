package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	titlesLibrary = "studio/movies"
	titlesFolder  = "Paper Lanterns (2019)"
	titlesFile    = "Paper Lanterns (2019).mkv"
)

// A stand-in for the titles binary. It appends each run's arguments to a
// file, one line per run, and returns the path of that file. A run that
// fails prints the words given on stderr, as the tool prints its error.
func installStandInTitles(t *testing.T, failure string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n"
	if failure != "" {
		script += "echo '" + failure + "' >&2\nexit 1\n"
	}
	binary := filepath.Join(dir, "titles")
	writeFile(t, binary, script)
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	held := titlesBinary
	t.Cleanup(func() { titlesBinary = held })
	titlesBinary = binary
	return calls
}

// One movie's video on the volume, and the item a gap holds for it.
func seedTitlesMovie(t *testing.T, root string) workItem {
	t.Helper()
	writeFile(t, filepath.Join(root, titlesFolder, titlesFile), "video")
	return workItem{Path: filepath.Join(titlesFolder, titlesFile), Size: int64(len("video")),
		DurationMs: 6000000, Listed: time.Now().UTC()}
}

func titlesLedgerOf(t *testing.T, folder string) likenLedger {
	t.Helper()
	ledger, err := readLikenLedger(folder, factTitles)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

// A main video with a length is the gap, whatever its title credits. A
// trailer and a video the probe has not reached are not.
func TestTheTitlesGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	video := func(path string, items ...string) fileRow {
		return fileRow{Path: path, Library: titlesLibrary, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", SizeBytes: 4096, Items: wholeItems(items...)}
	}
	trailer := video("A/trailers/Official Trailer.mp4", "movie:tmdb:1")
	trailer.Role = fileRoleTrailer
	unprobed := video("B/b.mkv", "movie:tmdb:2")
	unprobed.DurationMs, unprobed.VideoCodec = 0, ""
	seed := &walkResult{files: []fileRow{video("A/a.mkv", "movie:tmdb:1"), unprobed, trailer}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	items, err := catalog.workItems(t.Context(), factTitles, titlesLibrary, ledgerTime, reopenTime{})
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 1 || items[0].Path != "A/a.mkv" || items[0].Size != 4096 || items[0].DurationMs != 6000000 {
		t.Errorf("gap = %+v, want the one probed main video with its size and its length", items)
	}
}

// A found attempt closes the file for good, because the text on screen in
// one file does not change. An error is asked again after a day.
func TestATitlesAttemptHoldsItsFileOutOfTheGap(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name   string
		result string
		ago    time.Duration
		gap    bool
	}{
		{name: "found two months ago", result: attemptFound, ago: 60 * day},
		{name: "an error an hour ago", result: attemptError, ago: time.Hour},
		{name: "an error two days ago", result: attemptError, ago: 2 * day, gap: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seed := &walkResult{
				files: []fileRow{{Path: "A/a.mkv", Library: titlesLibrary, Present: true, Type: fileTypeVideo,
					Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", Items: wholeItems("movie:tmdb:1")}},
				attempts: []attemptRow{{Library: titlesLibrary, Item: "A/a.mkv", Fact: factTitles,
					At: ledgerTime.Add(-test.ago).Unix(), Result: test.result}},
			}
			if err := upsertWalk(t.Context(), catalog, seed); err != nil {
				t.Fatal(err)
			}

			items, err := catalog.workItems(t.Context(), factTitles, titlesLibrary, ledgerTime, reopenTime{})
			if err != nil {
				t.Fatal(err)
			}

			if (len(items) == 1) != test.gap {
				t.Errorf("gap = %+v, want the video in it: %v", items, test.gap)
			}
		})
	}
}

// The read of one video: the last 15 minutes of a film and the last 6 of an
// episode, in software with no render node and on the node with one, and a
// found attempt in the folder's ledger.
func TestTheTitlesWorkerReadsTheEndOfAVideo(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		nodes []string
		run   string
	}{
		{name: "a film in software", kind: libraryKindMovies,
			run: "read --window 900 --threads 2 --decode-threads 2 "},
		{name: "an episode in software", kind: libraryKindSeries,
			run: "read --window 360 --threads 2 --decode-threads 2 "},
		{name: "a film on the render node", kind: libraryKindMovies, nodes: []string{"renderD128"},
			run: "read --window 900 --threads 2 --decode-threads 2 --hwaccel vaapi "},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			item := seedTitlesMovie(t, root)
			seedRenderNodes(t, test.nodes...)
			calls := installStandInTitles(t, "")
			work, log := testFactWorker(t, titlesWorker, test.kind, root)

			work.titlesOne(t.Context(), item)

			video := filepath.Join(root, titlesFolder, titlesFile)
			runs := appearancesCalls(t, calls)
			if want := []string{test.run + video}; !slices.Equal(runs, want) {
				t.Errorf("runs = %q, want %q", runs, want)
			}
			folder, entry := likenFolderFor(test.kind, video)
			ledger := titlesLedgerOf(t, folder)
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound || ledger.Attempts[0].Path != entry {
				t.Errorf("attempts = %+v, want one found attempt at the file", ledger.Attempts)
			}
			if strings.Contains(log.String(), "Paper Lanterns") {
				t.Errorf("log = %q, want no title", log)
			}
		})
	}
}

// A read that fails records an error with the tool's own words, so a person
// reads the cause from the ledger.
func TestAFailedTitlesReadRecordsTheToolsWords(t *testing.T) {
	root := t.TempDir()
	item := seedTitlesMovie(t, root)
	seedRenderNodes(t)
	installStandInTitles(t, "titles: ffmpeg exited with exit status: 1")
	work, log := testFactWorker(t, titlesWorker, libraryKindMovies, root)

	work.titlesOne(t.Context(), item)

	want := "ffmpeg exited with exit status: 1"
	ledger := titlesLedgerOf(t, filepath.Join(root, titlesFolder))
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError ||
		!strings.Contains(ledger.Attempts[0].Reason, want) {
		t.Errorf("attempts = %+v, want one error that says %q", ledger.Attempts, want)
	}
	if !strings.Contains(log.String(), want) {
		t.Errorf("log = %q, want the tool's words %q", log, want)
	}
}

// A worker Job that is deleted or replaced stops its run in the middle of a
// video. The video was not answered, so the run records no attempt, and it
// removes the partial reading its own pod left. A partial file of another
// host may belong to a run that is still writing, so it stays.
func TestAStoppedTitlesReadRecordsNothing(t *testing.T) {
	root := t.TempDir()
	item := seedTitlesMovie(t, root)
	seedRenderNodes(t)
	t.Setenv("HOSTNAME", "movies-titles-a1")
	records := filepath.Join(root, titlesFolder, likenDirectory, titlesRecordsDirectory)
	ours := filepath.Join(records, titlesFile+".jsonl.partial-movies-titles-a1-41")
	theirs := filepath.Join(records, titlesFile+".jsonl.partial-movies-titles-b2-41")
	writeFile(t, ours, "{}")
	writeFile(t, theirs, "{}")
	installStandInTitles(t, "")
	work, _ := testFactWorker(t, titlesWorker, libraryKindMovies, root)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	work.titlesOne(ctx, item)

	if ledger := titlesLedgerOf(t, filepath.Join(root, titlesFolder)); len(ledger.Attempts) != 0 {
		t.Errorf("attempts = %+v, want none", ledger.Attempts)
	}
	if left := namesIn(t, records); !slices.Equal(left, []string{filepath.Base(theirs)}) {
		t.Errorf("the records directory holds %v, want the other host's partial alone", left)
	}
}

// The reason an attempt keeps: the tool's stderr, cut to its end where it
// is long, and the error itself where the tool said nothing.
func TestTheReasonOfATitlesFailure(t *testing.T) {
	long := strings.Repeat("x", titlesReasonLimit) + "\ntitles: the last line"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "the tool's words", err: &titlesFailure{err: os.ErrInvalid, stderr: "titles: no video\n"}, want: "titles: no video"},
		{name: "a long stderr", err: &titlesFailure{err: os.ErrInvalid, stderr: long}, want: long[len(long)-titlesReasonLimit:]},
		{name: "no stderr", err: &titlesFailure{err: os.ErrInvalid}, want: "titles read: invalid argument: "},
		{name: "another error", err: os.ErrNotExist, want: "file does not exist"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := titlesReason(test.err); got != test.want {
				t.Errorf("reason = %q, want %q", got, test.want)
			}
		})
	}
}

// The worker's Job runs the titles image, with the GPU claim where the
// Library names a template and no scratch volume.
func TestATitlesWorkerJob(t *testing.T) {
	cases := []struct {
		name     string
		template string
	}{
		{name: "in software"},
		{name: "on a render node", template: "decode"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Titles = LibraryTitles{Enabled: true, GPUResourceClaimTemplate: test.template, Parallelism: 2}

			job := buildFactWorkerJob(library, titlesWorker,
				jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, titles: testTitlesImage},
				testJobBus, "", enrichedRun(), 3, testNow)

			pod := job.Spec.Template.Spec
			container := pod.Containers[0]
			if container.Name != factTitles || container.Image != testTitlesImage || container.Command[1] != workerMode {
				t.Errorf("container = %s on %s running %v, want the titles worker", container.Name,
					container.Image, container.Command)
			}
			if container.Resources.Limits["memory"] != titlesMemoryLimit || len(pod.Volumes) != 1 {
				t.Errorf("memory limit = %q with %d volumes, want %s and the library's volume alone",
					container.Resources.Limits["memory"], len(pod.Volumes), titlesMemoryLimit)
			}
			if *job.Spec.Parallelism != 2 {
				t.Errorf("parallelism = %d, want the Library's 2", *job.Spec.Parallelism)
			}
			claimed := len(pod.ResourceClaims) == 1 && pod.ResourceClaims[0].ResourceClaimTemplateName == "decode"
			if claimed != (test.template != "") {
				t.Errorf("claims = %+v, want the template: %v", pod.ResourceClaims, test.template != "")
			}
		})
	}
}
