package main

// The bridge crops a trickplay tile from a Jellyfin sprite sheet, so a scrub
// shows the frame at the target time. The library precomputes the sheets, so
// the playback machine decodes no second video stream for a thumbnail. The
// display maps the scrub time to a tile and asks for it, and the bridge reads
// the geometry off the sheet, crops the cell, and scales it to the box.

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// serveTrickplay maps the cursor time to a tile, reads the sheet geometry off
// disk, crops the cell, scales it to the box, and replies. An item with no
// trickplay reference replies nothing.
func (c *commander) serveTrickplay(request artRequest) {
	intervalMs := trickplayIntervalMs()
	idx := request.timeMs / intervalMs

	c.artMutex.Lock()
	item := c.artItem
	// A request that maps to the tile already written replies with that blob, so
	// the overlay does not churn during a scrub.
	if c.trickHave && c.trickItem == item && c.trickIdx == idx {
		blob := c.trickBlob
		c.artMutex.Unlock()
		c.replyArt(artKindTrickplay, blob)
		return
	}
	block := c.blockForItem(item)
	c.artMutex.Unlock()

	dir := trickplayOf(block)
	if dir == "" {
		return
	}

	layoutName, tileW, cols, rows, ok := findLayout(dir)
	if !ok {
		fmt.Fprintf(os.Stderr, "command: trickplay %q: no layout directory\n", dir)
		return
	}
	layoutDir := filepath.Join(dir, layoutName)

	perSheet := cols * rows
	sheet := idx / perSheet
	cell := idx % perSheet
	row := cell / cols
	col := cell % cols
	// A time past the end of the film maps past the last sheet. The highest
	// sheet on disk is the last one, so clamp to it.
	if highest := highestSheet(layoutDir); sheet > highest {
		sheet = highest
	}

	sheetImg, err := c.trickplaySheet(item, layoutDir, sheet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: trickplay sheet %d in %q: %v\n", sheet, layoutDir, err)
		return
	}
	if sheetImg == nil {
		return
	}

	bounds := sheetImg.Bounds()
	// The tile height is the sheet height over the rows. It is the film's aspect,
	// so a scope film gives a short wide tile, not a 16:9 one.
	tileH := bounds.Dy() / rows
	if tileH <= 0 {
		return
	}
	x0 := bounds.Min.X + col*tileW
	y0 := bounds.Min.Y + row*tileH
	region := image.Rect(x0, y0, x0+tileW, y0+tileH)

	pixels, outW, outH, stride, err := scaleRegionToBGRA(sheetImg, region, request.width, request.height)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: trickplay crop in %q: %v\n", layoutDir, err)
		return
	}

	path := filepath.Join(c.artDir, fmt.Sprintf("trick-%d-%d-%d-%dx%d.bgra", item, sheet, cell, request.width, request.height))
	if err := os.WriteFile(path, pixels, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "command: trickplay write %q: %v\n", path, err)
		return
	}
	blob := artBlob{path: path, width: outW, height: outH, stride: stride}

	c.artMutex.Lock()
	if item != c.artItem {
		// The item swapped while the crop ran. Drop the blob, so the last item's
		// tile does not stay on the shared volume.
		c.artMutex.Unlock()
		os.Remove(blob.path)
		return
	}
	// Remove a tile file one step late. A reply tells mpv to map a tile on a
	// later turn, so removing the file the last reply named races that map and
	// drops the frame. The current tile becomes the prior tile, and only the
	// tile two steps back is removed. The guards skip the removal when the new
	// tile or the prior tile still names that file, so a scrub that reverses
	// never deletes a file in use.
	var stale artBlob
	haveStale := false
	if c.trickHave && c.trickItem == item && c.trickBlob.path != blob.path {
		if c.trickHavePrev && c.trickPrev.path != blob.path && c.trickPrev.path != c.trickBlob.path {
			stale = c.trickPrev
			haveStale = true
		}
		c.trickPrev = c.trickBlob
		c.trickHavePrev = true
	}
	c.trickHave = true
	c.trickItem = item
	c.trickIdx = idx
	c.trickBlob = blob
	c.artMutex.Unlock()

	if haveStale {
		os.Remove(stale.path)
	}
	c.replyArt(artKindTrickplay, blob)
}

// trickplaySheet returns the held sheet when the request names it, and decodes
// it once and holds it otherwise. It holds one sheet, because a scrub stays
// within one sheet for a long stretch, one hundred tiles at the interval. An
// item swap during the decode drops the result and returns nil.
func (c *commander) trickplaySheet(item int, layoutDir string, sheet int) (image.Image, error) {
	key := layoutDir + ":" + strconv.Itoa(sheet)

	c.artMutex.Lock()
	if c.trickSheetKey == key && c.trickSheet != nil {
		held := c.trickSheet
		c.artMutex.Unlock()
		return held, nil
	}
	c.artMutex.Unlock()

	path := filepath.Join(layoutDir, strconv.Itoa(sheet)+".jpg")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	c.artMutex.Lock()
	defer c.artMutex.Unlock()
	if item != c.artItem {
		return nil, nil
	}
	c.trickSheet = decoded
	c.trickSheetKey = key
	return decoded, nil
}

// trickplayIntervalMs reads the pod's interval as milliseconds. An empty or
// unparseable value, or one under a millisecond, falls back to
// defaultTrickplayInterval, because the tile mapping divides by this value.
func trickplayIntervalMs() int {
	duration, err := time.ParseDuration(os.Getenv(trickplayIntervalVariable))
	if err != nil || duration < time.Millisecond {
		duration, _ = time.ParseDuration(defaultTrickplayInterval)
	}
	return int(duration / time.Millisecond)
}

// trickplayOf reads the trickplay directory path from one presentation block,
// the way logoOf reads the logo. An empty block, or one with no trickplay, has
// none.
func trickplayOf(block []byte) string {
	var presentation Presentation
	if err := json.Unmarshal(block, &presentation); err != nil {
		return ""
	}
	return presentation.Trickplay
}

// findLayout finds the one layout directory inside a trickplay directory. Its
// name carries the tile width and the grid, like "320 - 10x10", so the bridge
// reads the geometry from the name and opens no sheet to learn it.
func findLayout(dir string) (name string, tileW, cols, rows int, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, 0, 0, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tileW, cols, rows, ok = parseLayout(entry.Name())
		if ok {
			return entry.Name(), tileW, cols, rows, true
		}
	}
	return "", 0, 0, 0, false
}

// parseLayout reads a layout name like "320 - 10x10" into the tile width, the
// columns, and the rows.
func parseLayout(name string) (tileW, cols, rows int, ok bool) {
	left, right, found := strings.Cut(name, " - ")
	if !found {
		return 0, 0, 0, false
	}
	tileW, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil || tileW <= 0 {
		return 0, 0, 0, false
	}
	columns, lines, found := strings.Cut(strings.TrimSpace(right), "x")
	if !found {
		return 0, 0, 0, false
	}
	cols, err = strconv.Atoi(columns)
	if err != nil || cols <= 0 {
		return 0, 0, 0, false
	}
	rows, err = strconv.Atoi(lines)
	if err != nil || rows <= 0 {
		return 0, 0, 0, false
	}
	return tileW, cols, rows, true
}

// highestSheet returns the highest N among the N.jpg sheets. A time past the
// end of the film clamps to it.
func highestSheet(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	highest := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jpg") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimSuffix(name, ".jpg"))
		if err != nil || number < 0 {
			continue
		}
		if number > highest {
			highest = number
		}
	}
	return highest
}
