package main

// What these tests read: the stand-ins for ffmpeg and ffprobe, what each
// failure of one pull leaves behind, and the write that never replaces a file
// that exists.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// The mark a body opens with where the stand-in ffmpeg reads it as a video.
const trailerBodyMark = "video"

// A stand-in for ffmpeg on PATH. It copies its input onto its output where
// the input opens with that mark, and fails the way the real one fails on a
// body that is not a video.
func standInFFmpegRemux(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffmpeg"),
		"#!/bin/sh\nfor last; do :; done\nin=\"\"\n"+
			"while [ $# -gt 0 ]; do if [ \"$1\" = \"-i\" ]; then in=\"$2\"; fi; shift; done\n"+
			"case \"$(head -c 5 \"$in\")\" in "+trailerBodyMark+") ;; "+
			"*) echo 'Invalid data found when processing input' >&2; exit 1;; esac\n"+
			"cp \"$in\" \"$last\"\n")
	if err := os.Chmod(filepath.Join(dir, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A stand-in for ffprobe. It answers the streams and the length a case names,
// so no test opens a real container.
func standInProbe(t *testing.T, videos int, seconds float64) {
	t.Helper()
	was := probeFile
	t.Cleanup(func() { probeFile = was })
	probeFile = func(context.Context, string) ([]byte, error) {
		streams := make([]string, videos)
		for at := range streams {
			streams[at] = `{"codec_type":"video","codec_name":"h264","height":1080}`
		}
		return fmt.Appendf(nil, `{"streams":[%s],"format":{"duration":"%s"}}`,
			strings.Join(streams, ","), strconv.FormatFloat(seconds, 'f', -1, 64)), nil
	}
}

// Every failure records an attempt, lands nothing, and leaves no temporary
// behind.
func TestAFailedTrailerFilePullLandsNothing(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		heights []int
		videos  int
		seconds float64
		fail    error
		want    string
	}{
		{
			name: "the site answers a page and not a video", body: "<html>no</html>",
			heights: []int{1080}, videos: 1, seconds: 120, want: attemptError,
		},
		{
			name: "the file is shorter than a trailer", body: "video bytes",
			heights: []int{1080}, videos: 1, seconds: 4, want: attemptError,
		},
		{
			name: "the file is longer than a trailer", body: "video bytes",
			heights: []int{1080}, videos: 1, seconds: 4000, want: attemptError,
		},
		{
			name: "the file holds no video stream", body: "video bytes",
			heights: []int{1080}, videos: 0, seconds: 120, want: attemptError,
		},
		{
			name: "the site refuses the file list", body: "video bytes",
			heights: []int{1080}, videos: 1, seconds: 120,
			fail: errors.New("the site is down"), want: attemptError,
		},
		{
			name: "the site holds no video file at all", body: "video bytes",
			videos: 1, seconds: 120, want: attemptNothing,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			standInFFmpegRemux(t)
			standInProbe(t, test.videos, test.seconds)
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			seedTrailerFileRun(t, catalog, root)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			line := trailerFetchLineOf(t, test.body, test.heights, test.fail)

			if err := work.trailerFileGap(t.Context(), line); err != nil {
				t.Fatal(err)
			}

			if held := trailersFolderHolds(t, root); len(held) != 0 {
				t.Errorf("the folder holds %v, want nothing", held)
			}
			ledger := trailerFileLedger(t, root)
			if ledger.TrailerFile != nil {
				t.Errorf("the ledger records %+v, want no file", ledger.TrailerFile)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != test.want {
				t.Errorf("attempts = %+v, want one %s", ledger.Attempts, test.want)
			}
		})
	}
}

// A file that already exists under the name is never written over.
func TestATrailerFileNeverLandsOverAFileThatStands(t *testing.T) {
	standInFFmpegRemux(t)
	standInProbe(t, 1, 120)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	standing := filepath.Join(root, trailerFileFolder, trailersFolderName, "Official Trailer.mp4")
	writeFile(t, standing, "the file a person kept")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line := trailerFetchLineOf(t, "video bytes", []int{1080}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	held, err := os.ReadFile(standing)
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != "the file a person kept" {
		t.Errorf("the file holds %q, want the bytes that were there", held)
	}
	if names := trailersFolderHolds(t, root); !slices.Equal(names, []string{"Official Trailer.mp4"}) {
		t.Errorf("the folder holds %v, want the one file and no temporary", names)
	}
	ledger := trailerFileLedger(t, root)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the attempt that found the file", ledger.Attempts)
	}
}

// A file that already exists under the name is read before the pull, so the
// site answers no bytes at all.
func TestATrailerFileThatStandsIsNeverPulled(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	standing := filepath.Join(root, trailerFileFolder, trailersFolderName, "Official Trailer.mp4")
	writeFile(t, standing, "the file a person kept")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line, answered := trailerFetchLineOfFiles(t, "video bytes",
		[]trailerFile{{Height: 1080}}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if answered.Load() != 0 {
		t.Errorf("the site answered %d bytes, want none", answered.Load())
	}
	if names := trailersFolderHolds(t, root); !slices.Equal(names, []string{"Official Trailer.mp4"}) {
		t.Errorf("the folder holds %v, want the one file and no temporary", names)
	}
	ledger := trailerFileLedger(t, root)
	if ledger.TrailerFile != nil {
		t.Errorf("the ledger records %+v, want no file", ledger.TrailerFile)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the attempt that found the file", ledger.Attempts)
	}
}

// The limit one pull runs under, for the length of one test.
func trailerPullLimitOf(t *testing.T, limit int64) {
	t.Helper()
	was := trailerPullLimit
	t.Cleanup(func() { trailerPullLimit = was })
	trailerPullLimit = limit
}

// A site that answers more than the limit is an error, and the temporary the
// stream wrote is removed with it.
func TestAPullOverTheLimitRecordsAnErrorAndLeavesNoTemporary(t *testing.T) {
	trailerPullLimitOf(t, 4)
	standInFFmpegRemux(t)
	standInProbe(t, 1, 120)
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedTrailerFileRun(t, catalog, root)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	line, _ := trailerFetchLineOfFiles(t, "video bytes over the limit",
		[]trailerFile{{Height: 1080}}, nil)

	if err := work.trailerFileGap(t.Context(), line); err != nil {
		t.Fatal(err)
	}

	if held := trailersFolderHolds(t, root); len(held) != 0 {
		t.Errorf("the folder holds %v, want nothing", held)
	}
	ledger := trailerFileLedger(t, root)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want the error the limit gave", ledger.Attempts)
	}
}

// A filesystem that makes no hard link lands the file through a create and a
// copy, and a file that exists is the same answer either way.
func TestTheTrailerDoorCopiesWhereItCannotLink(t *testing.T) {
	cases := []struct {
		name     string
		standing string
		want     string
	}{
		{name: "no file stands under the name", want: "the remuxed bytes"},
		{name: "a file already stands under the name",
			standing: "the file a person kept", want: "the file a person kept"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			was := linkFile
			t.Cleanup(func() { linkFile = was })
			linkFile = func(string, string) error {
				return &os.LinkError{Op: "link", Err: syscall.EXDEV}
			}
			root := t.TempDir()
			writer := newVolumeWriter("movies-trailers")
			temporary := writer.hiddenTemporary(root, trailerRemuxMark)
			writeFile(t, temporary, "the remuxed bytes")
			target := filepath.Join(root, "Official Trailer.mp4")
			if test.standing != "" {
				writeFile(t, target, test.standing)
			}

			landed, err := writer.createOnceFrom(temporary, target)
			if err != nil {
				t.Fatal(err)
			}

			if landed != (test.standing == "") {
				t.Errorf("the door answered %v, want %v", landed, test.standing == "")
			}
			if held := readFileString(t, target); held != test.want {
				t.Errorf("the file holds %q, want %q", held, test.want)
			}
		})
	}
}

// A link that fails for a reason the create fails for too is the error the
// attempt records.
func TestTheTrailerDoorReportsACreateItCannotMake(t *testing.T) {
	was := linkFile
	t.Cleanup(func() { linkFile = was })
	linkFile = func(string, string) error {
		return &os.LinkError{Op: "link", Err: syscall.EXDEV}
	}
	root := t.TempDir()
	writer := newVolumeWriter("movies-trailers")
	temporary := writer.hiddenTemporary(root, trailerRemuxMark)
	writeFile(t, temporary, "the remuxed bytes")
	target := filepath.Join(root, "a folder", "Official Trailer.mp4")
	writeFile(t, filepath.Join(root, "a folder"), "not a folder at all")

	landed, err := writer.createOnceFrom(temporary, target)

	if landed || err == nil {
		t.Errorf("landed = %v, err = %v, want the error the volume gave", landed, err)
	}
}

// A file that names a way this image does not hold is an error, and nothing
// lands.
func TestAFileWhoseWayThisImageDoesNotHoldIsAnError(t *testing.T) {
	source, address := newFakeDownload(t, &fakeDownload{body: "video bytes"})

	_, err := pullTrailerBytes(t.Context(), source,
		trailerFile{URL: address, Pull: "torrent"}, filepath.Join(t.TempDir(), ".pull"))

	if err == nil || !strings.Contains(err.Error(), "torrent") {
		t.Errorf("err = %v, want the one that names the way", err)
	}
}

// The write refuses a path that carries no temporary mark, so this fact can
// never link a file a person wrote into place.
func TestTheTrailerDoorRefusesAPathWithNoTemporaryMark(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a person's file.mp4"), "kept")
	writer := newVolumeWriter("movies-trailers")

	landed, err := writer.createOnceFrom(filepath.Join(root, "a person's file.mp4"),
		filepath.Join(root, "landed.mp4"))

	if landed || err == nil || !strings.Contains(err.Error(), likenTempMark) {
		t.Errorf("landed = %v, err = %v, want the refusal that names the mark", landed, err)
	}
}
