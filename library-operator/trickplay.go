package main

// The trickplay fact's whole run: the sweep that removes the map earlier runs
// wrote, the gap of videos with a length and no tiles beside them, the ffmpeg
// pass over one of them, and the sheets created where none exist. The fact
// asks no provider, because the file alone answers it, and it writes nothing
// into the .nfo.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The name of the container that runs this fact.
const trickplayContainerName = "trickplay"

// How much memory this container may take, and the share of a core it asks
// for. Both are above the scanner's, because ffmpeg decodes a video where
// every other container reads rows and files.
const (
	trickplayMemoryLimit = "512Mi"
	trickplayCPURequest  = "500m"
)

// The gap. A video the probe gave a length to, with no trickplay directory
// beside it in the catalog, outside the retry window. The scanner writes the
// column from the directory it finds, so the tiles this fact writes close the
// gap on the next walk.
func trickplayGapSQL() string {
	return `SELECT path, duration_ms FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND duration_ms > 0 AND video_codec != '' ` +
		`AND ` + gapClause(factTrickplay, "path", `trickplay = ''`)
}

// One gap: the file to open, and the length the probe wrote, which is what
// says how many thumbnails cover it.
type trickplayGap struct {
	path     string
	duration time.Duration
}

// The work list, out of the local copy of the catalog, with the same query the
// reporter counts the gap with.
func (c *Catalog) trickplayGaps(ctx context.Context, library string,
	now, refresh time.Time) ([]trickplayGap, error) {
	var gaps []trickplayGap
	err := c.stream(ctx, gapQueries[factTrickplay], gapParams(factTrickplay, library, now, refresh),
		func(cells []any) error {
			if len(cells) < 2 {
				return nil
			}
			path, _ := cells[0].(string)
			if path == "" {
				return nil
			}
			gaps = append(gaps, trickplayGap{
				path:     path,
				duration: time.Duration(cellNumber(cells[1])) * time.Millisecond,
			})
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", factTrickplay, library, err)
	}
	return gaps, nil
}

// The whole run. A catalog read that fails ends the container, because the gap
// list is the work. A file ffmpeg cannot read records an error attempt, and
// the run carries on to the next file. The files run one at a time, so one
// ffmpeg holds the container's memory line.
func (e *enricher) trickplayFact(ctx context.Context) error {
	if err := e.sweepTrickplayMaps(ctx); err != nil {
		return err
	}
	gaps, err := e.catalog.trickplayGaps(ctx, e.library, time.Now().UTC(), e.refresh[factTrickplay])
	if err != nil {
		return err
	}
	written := 0
	for _, gap := range gaps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.inScope(gap.path) {
			continue
		}
		if e.trickplayOne(ctx, gap) {
			written++
		}
	}
	e.logf("wrote the trickplay of %d of the %d files that had none", written, len(gaps))
	return nil
}

// One file. The volume is read before ffmpeg runs, because a directory that
// landed since the last walk is the answer already and costs no decode, and
// the ledger records that the tiles were already there.
func (e *enricher) trickplayOne(ctx context.Context, gap trickplayGap) bool {
	absolute := filepath.Join(e.root, gap.path)
	folder, entry := likenFolderFor(e.kind, absolute)
	target := trickplayDirectory(absolute)
	if dirExists(target) {
		if e.dropTrickplayMap(filepath.Join(target, trickplayTilesFolder())) {
			e.logf("removed the trickplay map beside the sheets of %s", filepath.Base(absolute))
		}
		e.recordArt(folder, factTrickplay, entry, artProviderExisting, attemptFound)
		return false
	}
	// The line goes out before the decode, because a decode of a feature
	// runs for minutes with nothing else to say, and it names the decoder,
	// because nothing else in the log says whether the GPU took the work.
	e.logf("tiling %s, %s long, %s", filepath.Base(absolute), gap.duration.Round(time.Second), decoderName())
	result := e.buildTrickplay(ctx, absolute, target)
	e.recordArt(folder, factTrickplay, entry, "", result)
	return result == attemptFound
}

// The decode and the write. ffmpeg writes its sheets under a staging name that
// carries the temporary mark, and one rename lands the whole tree, so the
// directory a player reads holds every sheet of the title or does not exist. A
// run that ends before the rename leaves the staging alone on the volume, and
// the run that follows it clears that staging first.
func (e *enricher) buildTrickplay(ctx context.Context, input, target string) string {
	staging, err := e.writer.stageTree(target)
	if err != nil {
		e.logf("could not stage the trickplay of %s: %v", filepath.Base(input), err)
		return attemptError
	}
	defer func() {
		if err := e.writer.removeTemporaryTree(staging); err != nil {
			e.logf("could not clear %s: %v", staging, err)
		}
	}()

	sheets, result := e.stageTrickplay(ctx, input, staging)
	if result != attemptFound {
		return result
	}
	landed, err := e.writer.createTree(target)
	if err != nil {
		e.logf("could not write %s: %v", target, err)
		return attemptError
	}
	if landed {
		e.logf("wrote %d trickplay sheets under %s", sheets, target)
	}
	return attemptFound
}

// The staged tree, which is the whole directory a player reads. ffmpeg tiles
// its sheets straight into the folder that states the width and the grid, so
// no sheet is ever read back to be written again, and nothing else goes in
// the folder: a player reads the geometry off the folder's name.
func (e *enricher) stageTrickplay(ctx context.Context, input, staging string) (int, string) {
	tiles := filepath.Join(staging, trickplayTilesFolder())
	if err := os.MkdirAll(tiles, volumeDirectoryPerm); err != nil {
		e.logf("could not stage the trickplay of %s: %v", filepath.Base(input), err)
		return 0, attemptError
	}
	// A decode ffmpeg refuses is the file's own state, and not a fault of
	// the run, so it is a miss with a date and the long window applies: a file
	// that will not decode today will not decode tomorrow. A run a signal
	// ended is an error, and the error window applies.
	if err := ffmpegSheets(ctx, input, tiles); err != nil {
		e.logf("could not tile %s: %v", filepath.Base(input), err)
		if ffmpegRefused(err) {
			return 0, attemptNothing
		}
		return 0, attemptError
	}
	sheets, err := sheetsIn(tiles)
	if err != nil {
		e.logf("could not read the sheets of %s: %v", filepath.Base(input), err)
		return 0, attemptError
	}
	if len(sheets) == 0 {
		e.logf("ffmpeg read no frame of %s", filepath.Base(input))
		return 0, attemptNothing
	}
	return len(sheets), attemptFound
}

// The tiled videos of one library. The scanner writes the directory it found
// beside a video into the row, so the column names every video that has one,
// whoever made it.
func trickplayTiledSQL() string {
	return `SELECT path, trickplay FROM files ` +
		`WHERE library = ?1 AND type = '` + fileTypeVideo + `' AND present = 1 ` +
		`AND trickplay != ''`
}

// The sweep. Earlier runs of this fact wrote a WebVTT map beside the sheets,
// which no player reads and which Jellyfin counts as a thumbnail. The gap
// never reaches a tiled video, because a video with a directory is no gap, so
// the sweep reads the tiled videos on its own and removes the map under each.
// One stat per tiled video, and no decode.
func (e *enricher) sweepTrickplayMaps(ctx context.Context) error {
	tiled, removed := 0, 0
	err := e.catalog.stream(ctx, trickplayTiledSQL(), []any{e.library}, func(cells []any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(cells) < 2 {
			return nil
		}
		path, _ := cells[0].(string)
		directory, _ := cells[1].(string)
		if directory == "" || !e.inScope(path) {
			return nil
		}
		tiled++
		if e.dropTrickplayMap(filepath.Join(e.root, directory, trickplayTilesFolder())) {
			removed++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading the tiled files of %s: %w", e.library, err)
	}
	e.logf("removed the trickplay map of %d of the %d files that carry tiles", removed, tiled)
	return nil
}

// The map of one layout folder, removed where it exists. A removal the volume
// refuses is logged and changes no attempt, because the sheets beside it are
// still the answer.
func (e *enricher) dropTrickplayMap(layout string) bool {
	path := filepath.Join(layout, trickplayMapName)
	present, _ := fileExists(path)
	if !present {
		return false
	}
	if err := e.writer.removeTrickplayMap(path); err != nil {
		e.logf("could not remove %s: %v", path, err)
		return false
	}
	return true
}
