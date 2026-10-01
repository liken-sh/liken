package main

// The trickplay fact: the gap of videos with a length and no tiles beside
// them, the ffmpeg pass over one of them, and the sheets created where none
// exist. The fact asks no provider, because the file alone answers it, and
// it writes nothing into the .nfo. It runs in a worker Job of its own
// (factworkers.go), because one title decodes for minutes.

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// The name of the container that runs this fact, which is the fact's name, as
// every worker container's is.
const trickplayContainerName = factTrickplay

// How much memory this container may take, and the share of a core it asks
// for. Both are above the scanner's, because ffmpeg decodes a video where
// every other container reads rows and files.
const (
	trickplayMemoryLimit = "512Mi"
	trickplayCPURequest  = "500m"
)

// The trickplay worker. It runs where the Library turns the fact on, on the
// image that holds ffmpeg, and it decodes on the render node its claim
// allocates where the Library names a render block. With no render block it
// decodes in software.
var trickplayWorker = factWorker{
	fact:    factTrickplay,
	enabled: func(library *Library) bool { return library.Spec.Trickplay.Enabled },
	image:   func(images jobImages) string { return images.ffmpeg },
	resources: func() ResourceRequirements {
		return ResourceRequirements{
			Requests: map[string]string{"cpu": trickplayCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": trickplayMemoryLimit},
		}
	},
	render: func(library *Library) *RenderDevice { return library.Spec.Trickplay.Render },
	work:   func(ctx context.Context, run *factWorkerRun, item workItem) { run.trickplayOne(ctx, item) },
}

// The gap. A feature the probe gave a length to, with no trickplay directory
// beside it in the catalog, outside the retry window. The scanner writes the
// column from the directory it finds, so the tiles this fact writes close the
// gap on the next walk.
//
// A video whose role is not the feature is no gap, because a player reads a
// thumbnail track while a person scrubs a title, and a trailer, an extra, a
// sample, or a theme is not a title.
//
// The query selects the columns a work list carries, so the rows are the
// worker's list as they are.
func trickplayGapSQL() string {
	return `SELECT path, size_bytes, duration_ms FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND role = '` + fileRolePrimary + `' ` +
		`AND duration_ms > 0 AND video_codec != '' ` +
		`AND ` + gapClause(factTrickplay, "path", `trickplay = ''`)
}

// One file. The volume is read before ffmpeg runs, because a directory that
// landed since the last walk is the answer already and costs no decode, and
// the ledger records that the tiles were already there.
//
// A directory older than the second a new file took this path is the
// exception. Jellyfin writes its tiles after it reads a file, and so does
// this worker, so a directory made at or after that second was made from the
// file that is here now, whoever made it, and the worker keeps it. A
// directory made before it was made from the earlier file, and its thumbnails
// sit at that encode's times, so the worker decodes this file and replaces the
// whole directory.
func (w *factWorkerRun) trickplayOne(ctx context.Context, gap workItem) bool {
	absolute := filepath.Join(w.root, gap.Path)
	folder, entry := likenFolderFor(w.kind, absolute)
	target := trickplayDirectory(absolute)
	earlier, err := earlierFileAt(w.kind, absolute)
	if err != nil {
		w.logf("could not read the probe record of %s: %v", w.named(absolute), err)
		w.record(folder, entry, "", attemptError)
		return false
	}
	replacing := madeBefore(target, earlier)
	if dirExists(target) && !replacing {
		if w.dropTrickplayMap(filepath.Join(target, trickplayTilesFolder())) {
			w.logf("removed the trickplay map beside the sheets of %s", w.named(absolute))
		}
		w.record(folder, entry, artProviderExisting, attemptFound)
		return false
	}
	if replacing {
		w.logf("replacing the trickplay of %s, which was made from the file its path held before", w.named(absolute))
	}
	// The line goes out before the decode, because a decode of a feature
	// runs for minutes with nothing else to say, and it names the decoder,
	// because nothing else in the log says whether the GPU took the work.
	w.logf("tiling %s, %s long, %s", w.named(absolute), gap.duration().Round(time.Second), decoderName())
	result := w.buildTrickplay(ctx, absolute, target, earlier)
	w.record(folder, entry, "", result)
	return result == attemptFound
}

// The decode and the write. ffmpeg writes its sheets under a staging name that
// carries the temporary mark, and one rename lands the whole tree, so the
// directory a player reads holds every sheet of the title or does not exist. A
// run that ends before the rename leaves the staging alone on the volume, and
// the run that follows it clears that staging first. The landing takes the
// place of a directory older than earlier, the tiles of the file the path
// held before, and keeps any other.
func (w *factWorkerRun) buildTrickplay(ctx context.Context, input, target string, earlier int64) string {
	staging, err := w.writer.stageTree(target)
	if err != nil {
		w.logf("could not stage the trickplay of %s: %v", w.named(input), err)
		return attemptError
	}
	defer func() {
		if err := w.writer.removeTemporaryTree(staging); err != nil {
			w.logf("could not clear %s: %v", w.named(staging), err)
		}
	}()

	sheets, result := w.stageTrickplay(ctx, input, staging)
	if result != attemptFound {
		return result
	}
	landed, err := w.writer.replaceEarlierTree(target, earlier)
	if err != nil {
		w.logf("could not write %s: %v", w.named(target), err)
		return attemptError
	}
	if landed {
		w.logf("wrote %d trickplay sheets under %s", sheets, w.named(target))
	}
	return attemptFound
}

// The staged tree, which is the whole directory a player reads. ffmpeg tiles
// its sheets straight into the folder that states the width and the grid, so
// no sheet is ever read back to be written again, and nothing else goes in
// the folder: a player reads the geometry off the folder's name.
func (w *factWorkerRun) stageTrickplay(ctx context.Context, input, staging string) (int, string) {
	tiles := filepath.Join(staging, trickplayTilesFolder())
	if err := os.MkdirAll(tiles, volumeDirectoryPerm); err != nil {
		w.logf("could not stage the trickplay of %s: %v", w.named(input), err)
		return 0, attemptError
	}
	// A decode ffmpeg refuses is the file's own state, and not a fault of
	// the run, so it is a miss with a date and the long window applies: a file
	// that will not decode today will not decode tomorrow. A run a signal
	// ended is an error, and the error window applies.
	if err := ffmpegSheets(ctx, input, tiles); err != nil {
		w.logf("could not tile %s: %v", w.named(input), err)
		if ffmpegRefused(err) {
			return 0, attemptNothing
		}
		return 0, attemptError
	}
	sheets, err := sheetsIn(tiles)
	if err != nil {
		w.logf("could not read the sheets of %s: %v", w.named(input), err)
		return 0, attemptError
	}
	if len(sheets) == 0 {
		w.logf("ffmpeg read no frame of %s", w.named(input))
		return 0, attemptNothing
	}
	return len(sheets), attemptFound
}

// The map of one layout folder, removed where it exists. A removal the volume
// refuses is logged and changes no attempt, because the sheets beside it are
// still the answer.
func (w *factWorkerRun) dropTrickplayMap(layout string) bool {
	path := filepath.Join(layout, trickplayMapName)
	present, _ := fileExists(path)
	if !present {
		return false
	}
	if err := w.writer.removeTrickplayMap(path); err != nil {
		w.logf("could not remove %s: %v", w.named(path), err)
		return false
	}
	return true
}
