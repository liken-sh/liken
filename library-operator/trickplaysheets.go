package main

// The trickplay layout: the folder name Jellyfin writes beside a video, and
// the one ffmpeg call that tiles the thumbnails into sheets. The layout is
// Jellyfin's, read off the lab's own volume on 2026-09-03: <video
// base>.trickplay/<width> - <columns>x<rows>/<index>.jpg, with the grid in
// the folder name and the sheets numbered from zero. A player reads the
// geometry off the folder name and the interval is Jellyfin's, so the folder
// holds the sheets and nothing else. See
// https://forum.jellyfin.org/t-trickplay-location and
// https://jellyfin.org/docs/general/server/media/trickplay-images/.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The four numbers of the layout. Ten seconds is Jellyfin's own interval, 320
// px is its thumbnail width, and ten by ten is its grid, so a player that
// reads one library reads both.
const (
	trickplayInterval = 10 * time.Second
	trickplayWidth    = 320
	trickplayColumns  = 10
	trickplayRows     = 10
)

// The extension every sheet carries.
const sheetExtension = ".jpg"

// One file's bound, so a video the decoder will not finish cannot hold the
// container open. An hour is above the longest title the lab holds.
var ffmpegTimeout = time.Hour

// The directory the tiles of one video go beside it under, which is the file's
// own name with the extension replaced. names.go reads the same name back for
// the catalog's column.
func trickplayDirectory(absolute string) string {
	return strings.TrimSuffix(absolute, filepath.Ext(absolute)) + trickplayExtension
}

// The folder inside it, which states the width and the grid, so a second width
// is a second folder and neither reads the other's sheets.
func trickplayTilesFolder() string {
	return fmt.Sprintf("%d - %dx%d", trickplayWidth, trickplayColumns, trickplayRows)
}

// The one call that opens a video. One decode pass writes every sheet to its
// own file, so the frames of a whole title are never held in memory, and the
// container runs one of these at a time.
func ffmpegSheets(ctx context.Context, input, directory string) error {
	timed, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	// The height follows the source's own aspect, rounded to an even number,
	// which is what the JPEG encoder takes.
	filter := fmt.Sprintf("fps=1/%d,scale=%d:-2,tile=%dx%d",
		int(trickplayInterval.Seconds()), trickplayWidth, trickplayColumns, trickplayRows)
	arguments := []string{"-nostdin", "-loglevel", "error"}
	// Plain -hwaccel vaapi decodes on the node and downloads the frames, so the
	// filter chain and the JPEG encode stay in software and a codec the node
	// refuses falls back to software decoding.
	if node := renderNode(); node != "" {
		arguments = append(arguments, "-hwaccel", "vaapi", "-hwaccel_device", node)
	}
	arguments = append(arguments, "-i", input,
		"-an", "-sn", "-dn", "-vf", filter, "-qscale:v", "4",
		"-start_number", "0", "-f", "image2", filepath.Join(directory, "%d"+sheetExtension))
	command := exec.CommandContext(timed, "ffmpeg", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", filepath.Base(input), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// The directory the kernel puts a GPU's render nodes in, a variable so a test
// points the lookup at a directory of its own.
var renderNodeDirectory = "/dev/dri"

// The first render node of this machine, and an empty string where it holds
// none, which is the software path.
func renderNode() string {
	nodes, err := filepath.Glob(filepath.Join(renderNodeDirectory, "renderD*"))
	if err != nil || len(nodes) == 0 {
		return ""
	}
	return nodes[0]
}

// Whether ffmpeg ended the run itself, with an exit code, which is what it
// does for a file it cannot read. A run a signal ended, such as a kill for
// memory, has no exit code, and it says nothing about the file.
func ffmpegRefused(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() >= 0
}

// The sheets one run left, in the order ffmpeg numbered them. A name that is
// not a number is not a sheet, so a stray file in the staging directory never
// becomes a tile.
func sheetsIn(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != sheetExtension {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSuffix(name, sheetExtension)); err != nil {
			continue
		}
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return sheetIndex(a) - sheetIndex(b) })
	return names, nil
}

// A name that reached the list above parses, so a failure here is impossible
// and reads as the first sheet.
func sheetIndex(name string) int {
	index, _ := strconv.Atoi(strings.TrimSuffix(name, sheetExtension))
	return index
}
