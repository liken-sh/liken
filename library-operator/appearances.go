package main

// The appearances fact: which credited person is on screen at each keyframe
// of a feature, the first step of plan 75. The gap is the features whose cast
// can be matched and that hold no answer. The worker runs the appearances
// tool on each one (appearancestool.go) and writes the answer to
// .liken/appearances.yaml beside the file (appearancesledger.go). The fact
// asks no provider and writes nothing into the .nfo. It runs in a worker Job
// of its own (factworkers.go), because detect decodes a whole feature.

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// What the container asks for and the memory it may take. The limit covers
// the largest measured run with room to spare: the tool held up to 350 MB,
// and ffmpeg up to 860 MB on a 4K file decoded in software and 510 MB on
// VA-API. The models run on one CPU thread and ffmpeg decodes on two, so
// half a core is the share the container needs on most of a run.
const (
	appearancesMemoryLimit = "1536Mi"
	appearancesCPURequest  = "500m"
)

// The emptyDir the worker keeps the GPU's compiled kernels in. OpenVINO
// compiles the models' kernels for the GPU on its first start, which took
// seconds in the experiments, and reads them from this cache on every start
// after it. One worker Job works the whole list, so one compile serves every
// video of the list, and a cache that the pod takes with it costs one compile
// per list.
const appearancesScratch = "/var/cache/appearances"

// The appearances worker. It runs where the Library turns the fact on, on the
// image that holds the tool and the models, and it decodes and runs the
// models on the GPU its claim allocates where the Library names a render
// block. With no render block it does both on the CPU.
var appearancesWorker = factWorker{
	fact:    factAppearances,
	enabled: func(library *Library) bool { return library.Spec.Appearances.Enabled },
	image:   func(images jobImages) string { return images.appearances },
	resources: func() ResourceRequirements {
		return ResourceRequirements{
			Requests: map[string]string{"cpu": appearancesCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": appearancesMemoryLimit},
		}
	},
	render:  func(library *Library) *RenderDevice { return library.Spec.Appearances.Render },
	scratch: appearancesScratch,
	work:    func(ctx context.Context, run *factWorkerRun, item workItem) { run.appearancesOne(ctx, item) },
	quick:   appearancesQuick,
}

// Whether a video of the list needs only the match: a detections record of
// its size is on the volume. A refresh reopens videos the worker answered
// before, and each of those takes a second, against minutes for a decode.
// The order reads only the record's first line, and the work itself still
// checks the models before it skips the decode.
func appearancesQuick(run *factWorkerRun, item workItem) bool {
	header, read := readDetectionsHeader(filepath.Join(run.root, item.Path))
	return read && header.Size == item.Size
}

// The gap. A feature the probe gave a length to, whose title credits an actor
// with a headshot, with no answer: no found attempt, and no attempt inside
// its window. An episode's cast is its series' cast, because the credits fact
// credits the series. A title with no headshot in its cast is no gap, because
// no face in it can be named, and the gap opens when the headshot lands.
//
// A found attempt closes the file until the file is replaced, because the
// faces of one file do not change. The walk drops the attempt of a replaced
// file (settlefacts.go), and the gap opens again. A miss is asked again after
// the dated window, and an error after a day.
//
// The query selects the columns a work list carries, so the rows are the
// worker's list as they are.
func appearancesGapSQL() string {
	return `SELECT path, size_bytes, duration_ms FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND role = '` + fileRolePrimary + `' ` +
		`AND duration_ms > 0 AND video_codec != '' ` +
		`AND path IN (SELECT path FROM file_items WHERE library = ?1 AND item IN (` +
		castTitlesSQL() + ` UNION ALL SELECT id FROM episodes WHERE library = ?1 AND series IN (` +
		castTitlesSQL() + `))) ` +
		`AND ` + gapClause(factAppearances, "path",
		`path NOT IN (SELECT item FROM attempts WHERE library = ?1 AND `+attemptFactColumn+
			` = '`+factAppearances+`' AND result = '`+attemptFound+`')`)
}

// The titles that credit at least one actor whose entry holds a headshot.
func castTitlesSQL() string {
	return `SELECT c.item FROM credits AS c JOIN contributors AS p ` +
		`ON p.library = c.library AND p.path = c.contributor ` +
		`WHERE c.library = ?1 AND c.part = '` + creditPartActor + `' AND p.headshot = 1`
}

// One file. The detect pass runs unless a detections record of this file is
// on the volume already, then the match pass runs on the folder that holds
// the file. The answer and the attempt go into the folder's ledger in one
// write, whatever the outcome.
func (w *factWorkerRun) appearancesOne(ctx context.Context, item workItem) {
	absolute := filepath.Join(w.root, item.Path)
	folder, entry := likenFolderFor(w.kind, absolute)
	if detectionsCurrent(absolute, item.Size, os.Getenv(appearancesModelsVariable)) {
		w.logf("reading the faces found before in %s", w.named(absolute))
	} else if err := w.detectFaces(ctx, absolute, item); err != nil {
		w.recordAppearances(folder, entry, nil, err)
		return
	}
	output, err := runAppearances(ctx, appearancesMatchTimeout, matchArgs(filepath.Dir(absolute), w.castFile(absolute))...)
	if err != nil {
		w.logf("could not match the faces of %s: %v", w.named(absolute), err)
		w.recordAppearances(folder, entry, nil, err)
		return
	}
	answer, err := answerFrom(output, entry, filepath.Base(absolute), item.Size)
	if err != nil {
		w.logf("could not read the match of %s: %v", w.named(absolute), err)
		w.recordAppearances(folder, entry, nil, err)
		return
	}
	w.logf("named %s in %s, and %d of the %d credited actors cannot be matched",
		counted(len(answer.Observations), "face"), w.named(absolute), len(answer.Unmatched), len(answer.Gallery))
	w.recordAppearances(folder, entry, &answer, nil)
}

// The detect pass. The line goes out before it, because the pass runs for
// minutes with nothing else to say, and it names the decoder, because nothing
// else in the log says whether the GPU took the work. A decode the render
// node refuses runs again in software: VA-API decodes only the codecs the
// GPU supports, and the frames stay on the GPU for the scale, so ffmpeg
// cannot fall back by itself.
func (w *factWorkerRun) detectFaces(ctx context.Context, absolute string, item workItem) error {
	hardware := renderNode() != ""
	w.logf("finding faces in %s, %s long, %s", w.named(absolute), item.duration().Round(time.Second), decoderName())
	_, err := runAppearances(ctx, appearancesDetectTimeout, detectArgs(absolute, hardware)...)
	if err != nil && hardware && ctx.Err() == nil {
		w.logf("the detect pass on %s failed with the render node's decoder, and runs again with a software decode: %v",
			w.named(absolute), err)
		_, err = runAppearances(ctx, appearancesDetectTimeout, detectArgs(absolute, false)...)
	}
	if err != nil {
		w.logf("could not find the faces in %s: %v", w.named(absolute), err)
	}
	return err
}

// The credits file that names the cast of a video: the one in its title
// folder's .liken directory. For an episode that is the series folder, which
// is where the credits fact writes.
func (w *factWorkerRun) castFile(absolute string) string {
	title, held := titleFolderOf(w.root, w.kind, absolute)
	if !held {
		title = filepath.Dir(absolute)
	}
	return filepath.Join(title, likenDirectory, likenLedgerName(factCredits))
}

// The answer and the attempt, in one write of one file. A failure is an
// error attempt with the tool's own words, and the answer an earlier run
// wrote stays: the ledger says what the fact last found.
func (w *factWorkerRun) recordAppearances(folder, entry string, answer *appearancesEntry, failure error) {
	attempt := likenAttempt{Path: entry, At: time.Now().UTC(), Result: attemptFound}
	if failure != nil {
		attempt.Result, attempt.Reason = attemptError, appearancesReason(failure)
	}
	err := w.writer.updateLikenLedger(folder, factAppearances, func(ledger *likenLedger) {
		if answer != nil {
			ledger.noteAppearances(*answer)
		}
		ledger.noteAttempt(attempt)
	})
	if err != nil {
		w.logf("could not record the appearances attempt at %s: %v", w.named(filepath.Join(folder, entry)), err)
	}
}
