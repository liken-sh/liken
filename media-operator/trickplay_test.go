package main

// These tests cover the trickplay bridge: the layout parse, the cell mapping,
// the crop, and the coalesce on the tile index.

import (
	"bufio"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestParseLayout(t *testing.T) {
	cases := []struct {
		name  string
		tileW int
		cols  int
		rows  int
		ok    bool
	}{
		{name: "320 - 10x10", tileW: 320, cols: 10, rows: 10, ok: true},
		{name: "240 - 8x12", tileW: 240, cols: 8, rows: 12, ok: true},
		{name: "no dash 10x10", ok: false},
		{name: "320 - 10", ok: false},
		{name: "wide - 10x10", ok: false},
		{name: "320 - 0x10", ok: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			tileW, cols, rows, ok := parseLayout(each.name)
			mustMatch(t, ok, each.ok)
			mustMatch(t, tileW, each.tileW)
			mustMatch(t, cols, each.cols)
			mustMatch(t, rows, each.rows)
		})
	}
}

// findLayout reads the one layout directory, and highestSheet reads the top
// numbered sheet.
func TestFindLayoutAndHighestSheet(t *testing.T) {
	root := t.TempDir()
	layout := filepath.Join(root, "16 - 2x2")
	mustSucceed(t, os.MkdirAll(layout, 0o755))
	for _, sheet := range []string{"0.jpg", "1.jpg", "2.jpg"} {
		mustSucceed(t, os.WriteFile(filepath.Join(layout, sheet), []byte("x"), 0o644))
	}

	name, tileW, cols, rows, ok := findLayout(root)
	mustMatch(t, ok, true)
	mustMatch(t, name, "16 - 2x2")
	mustMatch(t, tileW, 16)
	mustMatch(t, cols, 2)
	mustMatch(t, rows, 2)
	mustMatch(t, highestSheet(layout), 2)
}

// A sheet of four colored cells proves a time in the second interval crops
// cell 1, the green one.
func TestServeTrickplayCropsTheMappedCell(t *testing.T) {
	root := writeSheet(t, map[int]color.RGBA{
		0: {R: 200, A: 255},
		1: {G: 200, A: 255},
		2: {B: 200, A: 255},
		3: {R: 200, G: 200, A: 255},
	})
	artDir := t.TempDir()
	t.Setenv(trickplayIntervalVariable, "10s")

	server, client := net.Pipe()
	defer server.Close()
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{
		mpv:           client,
		artDir:        artDir,
		artItem:       1,
		presentations: []json.RawMessage{trickBlock(root)},
	}

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 15_000, width: 32, height: 32})

	reply := waitForLine(t, lines)
	path, w, h, stride := parseArtReply(t, reply)
	mustMatch(t, w, 32)
	mustMatch(t, h, 32)
	mustMatch(t, stride, 128)

	b, g, r := centerPixel(t, path, w, h, stride)
	if !(g > 150 && r < 60 && b < 60) {
		t.Fatalf("center pixel bgra = (%d,%d,%d), want the green cell 1", b, g, r)
	}
}

// Two times that map to one tile reply with the same written blob.
func TestServeTrickplayCoalescesOnIdx(t *testing.T) {
	root := writeSheet(t, map[int]color.RGBA{0: {R: 200, A: 255}, 1: {G: 200, A: 255}, 2: {B: 200, A: 255}, 3: {A: 255}})
	artDir := t.TempDir()
	t.Setenv(trickplayIntervalVariable, "10s")

	server, client := net.Pipe()
	defer server.Close()
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{
		mpv:           client,
		artDir:        artDir,
		artItem:       1,
		presentations: []json.RawMessage{trickBlock(root)},
	}

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 21_000, width: 24, height: 24})
	first, _, _, _ := parseArtReply(t, waitForLine(t, lines))

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 29_000, width: 24, height: 24})
	second, _, _, _ := parseArtReply(t, waitForLine(t, lines))

	mustMatch(t, second, first)
}

// A new tile keeps the file the previous reply named, so mpv can still map it
// for overlay-add. The bridge removes a tile only after a later tile replaces
// it, and holds at most two tile files at once.
func TestServeTrickplayKeepsThePriorTileFile(t *testing.T) {
	root := writeSheet(t, map[int]color.RGBA{0: {R: 200, A: 255}, 1: {G: 200, A: 255}, 2: {B: 200, A: 255}, 3: {A: 255}})
	artDir := t.TempDir()
	t.Setenv(trickplayIntervalVariable, "10s")

	server, client := net.Pipe()
	defer server.Close()
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c := &commander{
		mpv:           client,
		artDir:        artDir,
		artItem:       1,
		presentations: []json.RawMessage{trickBlock(root)},
	}

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 5_000, width: 24, height: 24})
	first, _, _, _ := parseArtReply(t, waitForLine(t, lines))

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 15_000, width: 24, height: 24})
	second, _, _, _ := parseArtReply(t, waitForLine(t, lines))

	mustExist(t, first)
	mustExist(t, second)

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 25_000, width: 24, height: 24})
	third, _, _, _ := parseArtReply(t, waitForLine(t, lines))

	mustExist(t, third)
	mustExist(t, second)
	mustNotExist(t, first)
}

// A block with no trickplay reference writes nothing to the socket.
func TestServeTrickplayRepliesNothingWithoutADirectory(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	wrote := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 256)
		if _, err := server.Read(buffer); err == nil {
			wrote <- struct{}{}
		}
	}()

	c := &commander{
		mpv:           client,
		artItem:       1,
		presentations: []json.RawMessage{json.RawMessage(`{"title":"No art"}`)},
	}
	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 5_000, width: 24, height: 24})

	select {
	case <-wrote:
		t.Error("a block with no trickplay wrote to the socket")
	case <-time.After(100 * time.Millisecond):
	}
}

// writeSheet builds a "16 - 2x2" trickplay directory with a 0.jpg of four
// colored cells, one solid color per cell.
func writeSheet(t *testing.T, cells map[int]color.RGBA) string {
	t.Helper()
	root := t.TempDir()
	layout := filepath.Join(root, "16 - 2x2")
	mustSucceed(t, os.MkdirAll(layout, 0o755))

	sheet := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for cell, c := range cells {
		col := cell % 2
		row := cell / 2
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				sheet.Set(col*16+x, row*16+y, c)
			}
		}
	}
	file, err := os.Create(filepath.Join(layout, "0.jpg"))
	mustSucceed(t, err)
	defer file.Close()
	mustSucceed(t, jpeg.Encode(file, sheet, &jpeg.Options{Quality: 100}))
	return root
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("wanted %q to exist, got %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("wanted %q removed, got %v", path, err)
	}
}

func trickBlock(dir string) json.RawMessage {
	block, _ := json.Marshal(Presentation{Trickplay: dir})
	return block
}

func waitForLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(time.Second):
		t.Fatal("no reply reached mpv")
		return ""
	}
}

// parseArtReply reads the path, the width, the height, and the stride from a
// trickplay liken-art reply.
func parseArtReply(t *testing.T, line string) (path string, w, h, stride int) {
	t.Helper()
	var command mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(line), &command))
	args := command.Command
	if len(args) != 8 || args[2] != artReplyMessage || args[3] != artKindTrickplay {
		t.Fatalf("reply = %q, want a trickplay liken-art message", line)
	}
	return args[4].(string), atoi(t, args[5]), atoi(t, args[6]), atoi(t, args[7])
}

func atoi(t *testing.T, value any) int {
	t.Helper()
	number, err := strconv.Atoi(value.(string))
	mustSucceed(t, err)
	return number
}

// centerPixel reads the blue, green, and red bytes of the blob's center
// pixel.
func centerPixel(t *testing.T, path string, w, h, stride int) (b, g, r byte) {
	t.Helper()
	pixels, err := os.ReadFile(path)
	mustSucceed(t, err)
	offset := (h/2)*stride + (w/2)*4
	return pixels[offset+0], pixels[offset+1], pixels[offset+2]
}

// A directory the bridge cannot read as a trickplay set has no layout
// and no sheets, so a scrub over it crops nothing.
func TestFindLayoutAndHighestSheetOnDirectoriesThatCarryNoSheets(t *testing.T) {
	loose := t.TempDir()
	mustSucceed(t, os.WriteFile(filepath.Join(loose, "poster.jpg"), []byte("x"), 0o644))
	mustSucceed(t, os.MkdirAll(filepath.Join(loose, "extras"), 0o755))

	mixed := t.TempDir()
	mustSucceed(t, os.MkdirAll(filepath.Join(mixed, "sheets"), 0o755))
	for _, name := range []string{"notes.txt", "cover.png", "first.jpg"} {
		mustSucceed(t, os.WriteFile(filepath.Join(mixed, name), []byte("x"), 0o644))
	}

	t.Run("no directory names a layout", func(t *testing.T) {
		_, _, _, _, ok := findLayout(loose)
		mustMatch(t, ok, false)
	})
	t.Run("the directory is not there", func(t *testing.T) {
		_, _, _, _, ok := findLayout(filepath.Join(loose, "absent"))
		mustMatch(t, ok, false)
	})
	t.Run("no file is a numbered sheet", func(t *testing.T) {
		mustMatch(t, highestSheet(mixed), 0)
	})
	t.Run("the sheet directory is not there", func(t *testing.T) {
		mustMatch(t, highestSheet(filepath.Join(mixed, "absent")), 0)
	})
}

// A sheet directory the bridge cannot crop a tile from answers nothing,
// and the display then scrubs with no thumbnail.
func TestServeTrickplayAnswersNothingWhenItCannotCrop(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, c *commander)
	}{
		{
			name: "the item names no trickplay set",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{json.RawMessage(emptyPresentation)}
			},
		},
		{
			name: "the block is not a presentation",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{json.RawMessage(`"not a block"`)}
			},
		},
		{
			name: "no directory names a layout",
			setup: func(t *testing.T, c *commander) {
				c.presentations = []json.RawMessage{trickBlock(t.TempDir())}
			},
		},
		{
			name: "the sheet is not an image",
			setup: func(t *testing.T, c *commander) {
				root := t.TempDir()
				layout := filepath.Join(root, "16 - 2x2")
				mustSucceed(t, os.MkdirAll(layout, 0o755))
				mustSucceed(t, os.WriteFile(filepath.Join(layout, "0.jpg"), []byte("not an image"), 0o644))
				c.presentations = []json.RawMessage{trickBlock(root)}
			},
		},
		{
			name: "the sheet is too short to hold its rows",
			setup: func(t *testing.T, c *commander) {
				root := t.TempDir()
				layout := filepath.Join(root, "16 - 2x2")
				mustSucceed(t, os.MkdirAll(layout, 0o755))
				file, err := os.Create(filepath.Join(layout, "0.jpg"))
				mustSucceed(t, err)
				defer file.Close()
				mustSucceed(t, jpeg.Encode(file, image.NewRGBA(image.Rect(0, 0, 32, 1)), nil))
				c.presentations = []json.RawMessage{trickBlock(root)}
			},
		},
		{
			name: "the art directory is not there",
			setup: func(t *testing.T, c *commander) {
				c.artDir = filepath.Join(t.TempDir(), "absent")
				c.presentations = []json.RawMessage{trickBlock(writeSheet(t, map[int]color.RGBA{0: {R: 200, A: 255}}))}
			},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv(trickplayIntervalVariable, "10s")
			c, lines := bridgeToMPV(t)
			c.artItem = 1
			each.setup(t, c)

			c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 5_000, width: 24, height: 24})

			expectNoArtReply(t, lines)
		})
	}
}

// A time past the end of the film maps past the last sheet on disk, so
// the bridge clamps to the highest sheet and answers with a tile from it.
func TestServeTrickplayClampsATimePastTheLastSheet(t *testing.T) {
	t.Setenv(trickplayIntervalVariable, "10s")
	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{trickBlock(writeSheet(t, map[int]color.RGBA{0: {G: 200, A: 255}}))}

	go c.serveTrickplay(artRequest{kind: artKindTrickplay, timeMs: 9_000_000, width: 24, height: 24})

	path, w, h, stride := parseArtReply(t, waitForLine(t, lines))
	mustMatch(t, w, 24)
	mustExist(t, path)

	b, g, r := centerPixel(t, path, w, h, stride)
	if !(g > 150 && r < 60 && b < 60) {
		t.Fatalf("center pixel bgra = (%d,%d,%d), want the green cell 0 of the last sheet", b, g, r)
	}
}
