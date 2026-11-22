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

// The version of the definitions in this file, which the probe writes in
// each record that it measures. A record with measured: true and no version
// was written by the first version. That version counted a box behind the
// text, a picture, and two lines that touch as one line of text, so the
// probe measures each stream with its numbers again. A stream that had too
// few cues for the first version has too few for this one, because this
// version counts the same cues or fewer.
const imageMeasurement = 2

// A pixel is ink when its alpha is 128 or more of 255. A VobSub pixel has a
// 4-bit alpha, which the decoder scales by 17, so 8 or more of 15 is ink.
const inkAlpha = 128

// One row of a bitmap as a decoder reduces it: the largest alpha in the
// row, the number of its ink pixels, and the columns of its first and last
// ink pixel.
type bitmapRow struct {
	alpha uint8
	ink   int
	first int
	last  int
}

// paint adds a run of pixels of one alpha, from column x, to the row. A
// run of no pixels adds nothing.
func (r *bitmapRow) paint(x, run int, alpha uint8) {
	if run <= 0 {
		return
	}
	r.alpha = max(r.alpha, alpha)
	if alpha < inkAlpha {
		return
	}
	if r.ink == 0 {
		r.first = x
	}
	r.ink += run
	r.last = max(r.last, x+run-1)
}

// Whether every pixel from the row's first ink pixel to its last is ink. A
// row of letters has clear pixels between the letters, and a row of a box
// behind the text or of a picture usually has none.
func (r bitmapRow) gapless() bool {
	return r.ink > 0 && r.ink == r.last-r.first+1
}

// fewestLowerCues is the smallest number of lower cues that gives a stream
// its numbers. On the 24 tracks that plan 97 sampled, the ink bottom of any
// 10 consecutive lower cues was within 1 row of the ink bottom of 600
// seconds of cues.
const fewestLowerCues = 10

// A run of ink rows at least this many tenths of the median line is two or
// more lines that touch. Two lines touch only where a descender of the first
// meets an ascender or a capital of the second, so the run is nearly twice
// as tall as one line. A line of a script with marks above and below it,
// such as Thai, is taller than the median line of its track by less than
// that. Plan 97 has the tracks that set the limit.
const touchingLines = 17

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

// bitmapOf reduces one bitmap to its lines of text. rows has one row of the
// bitmap for each canvas row from y. FFmpeg crops a VobSub bitmap to its
// first row with any alpha before mpv receives it, so a cropped bitmap
// starts at that row. A PGS object keeps its transparent margin.
func bitmapOf(canvasHeight, y int, rows []bitmapRow, cropped bool) imageBitmap {
	bitmap := imageBitmap{canvasHeight: canvasHeight, top: y}
	if cropped {
		if first := slices.IndexFunc(rows, func(row bitmapRow) bool { return row.alpha > 0 }); first > 0 {
			bitmap.top = y + first
		}
	}
	start := -1
	for row := range len(rows) + 1 {
		ink := row < len(rows) && rows[row].ink > 0
		switch {
		case ink && start < 0:
			start = row
		case !ink && start >= 0:
			if isText(rows[start:row], canvasHeight) {
				bitmap.lines = append(bitmap.lines, inkLine{start: y + start, end: y + row})
			}
			start = -1
		}
	}
	return bitmap
}

// isText reports whether a run of ink rows can be a line of text. A run is
// a box behind the text or a picture when at least three of each four of
// its rows are gapless, or when it is taller than a quarter of the canvas.
// Neither tells the height of a line, so the run is in no number. A line of
// letters has gaps between its letters and its words in most of its rows,
// even with a thick outline. Plan 97 has the tracks that set the limits.
func isText(run []bitmapRow, canvasHeight int) bool {
	if 4*len(run) > canvasHeight {
		return false
	}
	gapless := 0
	for _, row := range run {
		if row.gapless() {
			gapless++
		}
	}
	return 4*gapless < 3*len(run)
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

// add counts one bitmap. A bitmap with no line of text is not a cue. A
// lower cue is a bitmap whose top row is at or below half the canvas height,
// because those are the only bitmaps that mpv moves with sub-pos (get_bitmaps
// in mpv's sub/sd_lavc.c). The stream's canvas is the canvas of its first
// cue.
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
// Two lines of text touch when the descenders of the first reach the
// ascenders of the second, and the run of ink rows is then about twice as
// tall as one line. A run at least touchingLines tenths of the median line
// is such a pair, or more, and it is in neither number.
//
// The line height is the 90th percentile, by nearest rank, of the heights of
// the other lines. The ink bottom is the median of the rows below the last
// lines of the lower cues whose last line is one line at least the line
// height less one row tall, because about a third of last lines have no
// descender and end 11 to 14 rows higher. With that rule the median settles
// after about 10 cues, and without it after about 80. When no lower cue has
// such a line, every lower cue counts.
func (c *imageCues) measurement() *probedImage {
	if len(c.lowerCues) < fewestLowerCues || c.canvasHeight <= 0 {
		return nil
	}
	heights := slices.Sorted(slices.Values(c.lineHeights))
	median := heights[len(heights)/2]
	oneLine := func(height int) bool { return 10*height < touchingLines*median }
	heights = slices.DeleteFunc(heights, func(height int) bool { return !oneLine(height) })
	lineHeight := heights[(9*len(heights)+9)/10-1]
	var full, every []int
	for _, cue := range c.lowerCues {
		every = append(every, cue.bottom)
		if cue.lastLine >= lineHeight-1 && oneLine(cue.lastLine) {
			full = append(full, cue.bottom)
		}
	}
	if len(full) == 0 {
		full = every
	}
	slices.Sort(full)
	return &probedImage{CanvasHeight: c.canvasHeight, LineHeight: lineHeight, InkBottom: full[len(full)/2]}
}
