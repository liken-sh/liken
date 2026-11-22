package main

// imagemeasure.go computes the measurement of an image subtitle stream, PGS
// or VobSub. The three numbers are in rows of the stream's own canvas, and
// media-operator reads them to draw the track's last line on the same row
// and at the same size as every other subtitle. Plan 97 defines each number
// from the bitmaps of a sample of cues. The decoders in pgsdecode.go and
// vobsubdecode.go reduce each bitmap to its top row and its lines, so the
// probe keeps a few numbers for each cue and no picture.

import (
	"slices"
)

// A pixel is ink when its alpha is 128 or more of 255. A VobSub pixel has a
// 4-bit alpha, which the decoder scales by 17, so 8 or more of 15 is ink.
const inkAlpha = 128

// fewestLowerCues is the smallest number of lower cues that gives a stream
// its numbers. On the 24 tracks that plan 97 sampled, the ink bottom of any
// 10 consecutive lower cues was within 1 row of the ink bottom of 600
// seconds of cues.
const fewestLowerCues = 10

// One line of text in a bitmap: a run of consecutive ink rows, from its
// first canvas row to the row just below its last.
type inkLine struct {
	start int
	end   int
}

// One bitmap, as mpv receives it, reduced to what the measurement reads.
// top is the canvas row of the bitmap's first row, and lines are its lines
// of text in canvas rows, from the top down.
type imageBitmap struct {
	canvasHeight int
	top          int
	lines        []inkLine
}

// bitmapOf reduces one bitmap to its lines. rows has the largest alpha of
// each of the bitmap's rows, and y is the canvas row of the first of them.
// FFmpeg crops a VobSub bitmap to its first row with any alpha before mpv
// receives it, so a cropped bitmap starts at that row. A PGS object keeps
// its transparent margin.
func bitmapOf(canvasHeight, y int, rows []uint8, cropped bool) imageBitmap {
	bitmap := imageBitmap{canvasHeight: canvasHeight, top: y}
	if cropped {
		if first := slices.IndexFunc(rows, func(alpha uint8) bool { return alpha > 0 }); first > 0 {
			bitmap.top = y + first
		}
	}
	start := -1
	for row, alpha := range rows {
		switch {
		case alpha >= inkAlpha && start < 0:
			start = row
		case alpha < inkAlpha && start >= 0:
			bitmap.lines = append(bitmap.lines, inkLine{start: y + start, end: y + row})
			start = -1
		}
	}
	if start >= 0 {
		bitmap.lines = append(bitmap.lines, inkLine{start: y + start, end: y + len(rows)})
	}
	return bitmap
}

// The cues of one image stream that the probe read, as the measurement needs
// them: the height of every line, and for each lower cue the height of its
// last line and the row just below it.
type imageCues struct {
	canvasHeight int
	lineHeights  []int
	lowerCues    []lowerCue
}

type lowerCue struct {
	lastLine int
	bottom   int
}

// add counts one bitmap. A bitmap with no ink row is not a cue. A lower cue
// is a bitmap whose top row is at or below half the canvas height, because
// those are the only bitmaps that mpv moves with sub-pos (get_bitmaps in
// mpv's sub/sd_lavc.c). The stream's canvas is the canvas of its first cue.
func (c *imageCues) add(bitmap imageBitmap) {
	if len(bitmap.lines) == 0 {
		return
	}
	if c.canvasHeight == 0 {
		c.canvasHeight = bitmap.canvasHeight
	}
	for _, line := range bitmap.lines {
		c.lineHeights = append(c.lineHeights, line.end-line.start)
	}
	if bitmap.top >= bitmap.canvasHeight/2 {
		last := bitmap.lines[len(bitmap.lines)-1]
		c.lowerCues = append(c.lowerCues, lowerCue{lastLine: last.end - last.start, bottom: last.end})
	}
}

// How many lower cues the probe has read.
func (c *imageCues) lower() int {
	return len(c.lowerCues)
}

// measurement gives the stream's three numbers, or nil when the probe read
// fewer than fewestLowerCues lower cues.
//
// The line height is the 90th percentile, by nearest rank, of the heights of
// every line. The ink bottom is the median of the rows below the last lines
// of the lower cues whose last line is at least the line height less one
// row, because about a third of last lines have no descender and end 11 to
// 14 rows higher. With that rule the median settles after about 10 cues, and
// without it after about 80. When no lower cue has such a line, every lower
// cue counts.
func (c *imageCues) measurement() *probedImage {
	if len(c.lowerCues) < fewestLowerCues || c.canvasHeight <= 0 {
		return nil
	}
	heights := slices.Sorted(slices.Values(c.lineHeights))
	lineHeight := heights[(9*len(heights)+9)/10-1]
	var full, every []int
	for _, cue := range c.lowerCues {
		every = append(every, cue.bottom)
		if cue.lastLine >= lineHeight-1 {
			full = append(full, cue.bottom)
		}
	}
	if len(full) == 0 {
		full = every
	}
	slices.Sort(full)
	return &probedImage{CanvasHeight: c.canvasHeight, LineHeight: lineHeight, InkBottom: full[len(full)/2]}
}
