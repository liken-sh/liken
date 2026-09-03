package main

// The trickplay layout: the folder name Jellyfin writes beside a video, the
// one ffmpeg call that tiles the thumbnails into sheets, and the WebVTT that
// maps a time range onto a region of one sheet. The layout is Jellyfin's, read
// off the lab's own volume on 2026-09-03: <video base>.trickplay/<width> -
// <columns>x<rows>/<index>.jpg, with the grid in the folder name and the
// sheets numbered from zero. Jellyfin serves the map from its API and writes
// none, so the WebVTT beside the sheets is this project's own. See
// https://forum.jellyfin.org/t-trickplay-location and
// https://jellyfin.org/docs/general/server/media/trickplay-images/.

import (
	"context"
	"fmt"
	"image"

	// Image.DecodeConfig reads a sheet's header only through the format that
	// registers itself here.
	_ "image/jpeg"
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

// One sheet holds the whole grid, padded where the video ends inside it.
const trickplayTilesPerSheet = trickplayColumns * trickplayRows

// The extension every sheet carries, and the name of the map beside them.
const (
	sheetExtension     = ".jpg"
	trickplayIndexName = "tiles.vtt"
)

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

func sheetName(index int) string {
	return strconv.Itoa(index) + sheetExtension
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
	command := exec.CommandContext(timed, "ffmpeg",
		"-nostdin", "-loglevel", "error", "-i", input,
		"-an", "-sn", "-dn", "-vf", filter, "-qscale:v", "4",
		"-start_number", "0", "-f", "image2", filepath.Join(directory, "%d"+sheetExtension))
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", filepath.Base(input), err, strings.TrimSpace(string(output)))
	}
	return nil
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

// The size of one thumbnail, out of the first sheet's own header. The grid is
// fixed, so the sheet's width and height divided by it are the tile, and no
// frame is decoded to learn them.
func tileSize(sheet string) (int, int, error) {
	file, err := os.Open(sheet)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("reading %s: %w", filepath.Base(sheet), err)
	}
	return config.Width / trickplayColumns, config.Height / trickplayRows, nil
}

// How many thumbnails cover a title of this length, bounded by what ffmpeg
// actually wrote, so the map never names a tile the last padded sheet holds no
// frame for.
func trickplayTiles(duration time.Duration, sheets int) int {
	tiles := int(duration / trickplayInterval)
	if duration%trickplayInterval > 0 {
		tiles++
	}
	return min(tiles, sheets*trickplayTilesPerSheet)
}

// The map itself. One cue per thumbnail, naming the sheet and the region of it
// the thumbnail sits in, and the last cue ends at the title's own end and not
// at the end of its ten seconds.
func trickplayVTT(tiles, tileWidth, tileHeight int, duration time.Duration) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n")
	for tile := range tiles {
		start := time.Duration(tile) * trickplayInterval
		end := min(start+trickplayInterval, duration)
		column := tile % trickplayColumns
		row := tile / trickplayColumns % trickplayRows
		fmt.Fprintf(&out, "\n%s --> %s\n%s#xywh=%d,%d,%d,%d\n",
			vttTimestamp(start), vttTimestamp(end), sheetName(tile/trickplayTilesPerSheet),
			column*tileWidth, row*tileHeight, tileWidth, tileHeight)
	}
	return []byte(out.String())
}

// The timestamp WebVTT states, hours to milliseconds, with every field padded,
// because a reader takes no short form.
func vttTimestamp(at time.Duration) string {
	milliseconds := at.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d.%03d", milliseconds/3_600_000,
		milliseconds/60_000%60, milliseconds/1_000%60, milliseconds%1_000)
}
