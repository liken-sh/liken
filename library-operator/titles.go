package main

// The titles fact: the text on screen in each second of the end of a video,
// which is what the end titles are placed from (plan 96). The gap is the
// main videos with a length and no reading. The worker runs `titles read`
// on each one (titlestool.go), and the tool writes the reading to
// .liken/titles/<file>.jsonl beside the file. The fact asks no provider and
// writes nothing into the .nfo. It runs in a worker Job of its own
// (factworkers.go), because the read decodes minutes of every video.

import (
	"context"
	"path/filepath"
	"time"
)

// What the container asks for and the memory it may take. Plan 96 holds the
// measured runs that the two values come from. The request is one core,
// because the tool runs the text models on two threads for part of each
// second of video and ffmpeg decodes beside it. There is no CPU limit, so a
// run uses the idle cores of its node.
const (
	titlesMemoryLimit = "1536Mi"
	titlesCPURequest  = "1"
)

// How much of the end of a video the tool reads, by the kind of Library. The
// end titles of a film can run past ten minutes, and those of an episode
// seldom pass two, so an episode's read is shorter and costs less.
const (
	titlesFilmWindow    = 15 * time.Minute
	titlesEpisodeWindow = 6 * time.Minute
)

// The titles worker. It runs where the Library turns the fact on, on the
// image that holds the tool and the text models. It decodes on the render
// node its claim allocates where the Library names a ResourceClaimTemplate,
// and in software with none. The text models run on the CPU in both cases,
// so the worker keeps no cache of GPU kernels and names no scratch
// directory.
var titlesWorker = factWorker{
	fact:    factTitles,
	enabled: func(library *Library) bool { return library.Spec.Titles.Enabled },
	image:   func(images jobImages) string { return images.titles },
	resources: func() ResourceRequirements {
		return ResourceRequirements{
			Requests: map[string]string{"cpu": titlesCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": titlesMemoryLimit},
		}
	},
	gpuClaimTemplate: func(library *Library) string { return library.Spec.Titles.GPUResourceClaimTemplate },
	parallelism:      func(library *Library) int { return podsOf(library.Spec.Titles.Parallelism) },
	work:             func(ctx context.Context, run *factWorkerRun, item workItem) { run.titlesOne(ctx, item) },
}

// The gap. A main video the probe gave a length to, with no answer: no found
// attempt, and no attempt inside its window. Every such video is in the gap,
// with a mark from a database or with none, because a model places the end
// titles of a video that no database has a mark for.
//
// A found attempt closes the file until the file is replaced, because the
// text on screen in one file does not change. The walk drops the attempt of
// a replaced file (settlefacts.go), and the gap opens again. An error is
// asked again after a day.
//
// The query selects the columns of a workItem, so the rows are the worker's
// gap as they are.
func titlesGapSQL() string {
	return `SELECT path, size_bytes, duration_ms FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND role = '` + fileRolePrimary + `' ` +
		`AND duration_ms > 0 AND video_codec != '' ` +
		`AND ` + gapClause(factTitles, "path",
		`path NOT IN (SELECT item FROM attempts WHERE library = ?1 AND `+attemptFactColumn+
			` = '`+factTitles+`' AND result = '`+attemptFound+`')`)
}

// How much of the end of a video of this Library the tool reads.
func titlesWindow(kind string) time.Duration {
	if kind == libraryKindSeries {
		return titlesEpisodeWindow
	}
	return titlesFilmWindow
}

// One file. The tool reads the end of the video and writes the reading, or
// leaves a reading of the same file by the same models as it is. The attempt
// goes into the folder's ledger, whatever the outcome.
//
// A worker Job that is deleted or replaced stops its run in the middle of a
// video, and the tool's error is then the kill and not a fact about the
// video. An attempt would answer the video and hold it out of the gap, so a
// stopped run records nothing, and the video goes to the next worker. The
// killed tool leaves its partial reading, and the run removes the ones this
// pod wrote.
func (w *factWorkerRun) titlesOne(ctx context.Context, item workItem) {
	absolute := filepath.Join(w.root, item.Path)
	folder, entry := likenFolderFor(w.kind, absolute)
	hardware := renderNode() != ""
	w.logf("reading the end of %s, %s long, %s", w.named(absolute), item.duration().Round(time.Second), decoderName())
	err := runTitles(ctx, readArgs(absolute, titlesWindow(w.kind), hardware)...)
	if err != nil && ctx.Err() != nil {
		w.logf("stopped in the middle of %s, which stays for the next worker", w.named(absolute))
		w.removeOwnPartials(filepath.Join(filepath.Dir(absolute), likenDirectory, titlesRecordsDirectory))
		return
	}
	attempt := likenAttempt{Path: entry, At: time.Now().UTC(), Result: attemptFound}
	if err != nil {
		w.logf("could not read the end of %s: %v", w.named(absolute), err)
		attempt.Result, attempt.Reason = attemptError, titlesReason(err)
	}
	if err := w.writer.updateLikenLedger(folder, factTitles, func(ledger *likenLedger) {
		ledger.noteAttempt(attempt)
	}); err != nil {
		w.logf("could not record the titles attempt at %s: %v", w.named(filepath.Join(folder, entry)), err)
	}
}
