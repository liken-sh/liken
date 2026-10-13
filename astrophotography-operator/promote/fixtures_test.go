package promote

// The fixtures of the promote mode's tests: FITS frames as the INDI
// driver writes them, and an outbox and an archive in t.TempDir().

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// card is one 80-character header card.
func card(keyword, value string) string {
	return fmt.Sprintf("%-8s= %-70s", keyword, value)
}

// fitsFrame answers a FITS file of a 4 x 4 image of 16-bit pixels with
// the cards given, each a card(), and the cards of every frame of this
// operator before them. The data take one block of 2,880 bytes, so the
// file takes two blocks.
func fitsFrame(frameID string, cards ...string) []byte {
	all := []string{
		card("SIMPLE", "T"), card("BITPIX", "16"), card("NAXIS", "2"),
		card("NAXIS1", "4"), card("NAXIS2", "4"),
	}
	if frameID != "" {
		all = append(all, card("FRAMEID", "'"+frameID+"' / frame identity"))
	}
	all = append(all, cards...)
	all = append(all, fmt.Sprintf("%-80s", "END"))
	header := []byte(strings.Join(all, ""))
	header = append(header, []byte(strings.Repeat(" ", 2880-len(header)%2880))...)
	data := make([]byte, 2880)
	copy(data, frameID)
	return append(header, data...)
}

// lightFrame is a frame of a Luminance sequence, 300 s at 1x1, gain 100
// and offset 10, as the driver writes it.
func lightFrame(frameID string) []byte {
	return fitsFrame(frameID,
		card("FILTER", "'Luminance'"), card("EXPTIME", "3.000000E+02"), card("XBINNING", "1"),
		card("GAIN", "100."), card("OFFSET", "10."), card("DATE-OBS", "'2026-10-12T23:15:06.864'"))
}

// world is an outbox and an archive, each the root of a claim.
type world struct {
	t       *testing.T
	outbox  string
	archive string
}

func newWorld(t *testing.T) *world {
	root := t.TempDir()
	return &world{t: t, outbox: filepath.Join(root, "outbox"), archive: filepath.Join(root, "archive")}
}

// options are the options of a run for the target m42 into astro/.
func (w *world) options() Options {
	return Options{
		Outbox: w.outbox, Archive: w.archive, Path: "astro", Target: "m42",
		Cameras: map[string]Camera{"east-main": {Telescope: "east", Train: "east-imaging"}},
	}
}

// expose writes a frame into the outbox under its name, the way the
// driver does, and answers its path relative to the outbox's root.
func (w *world) expose(name string, content []byte) string {
	rel := filepath.Join("east-main", "east-tonight", "m42", name+".fits")
	w.write(filepath.Join(w.outbox, rel), content)
	return rel
}

func (w *world) write(name string, content []byte) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// files answers every file under a root, relative to it, with its
// content.
func (w *world) files(root string) map[string]string {
	w.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(name)
		rel, _ := filepath.Rel(root, name)
		out[rel] = string(content)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		w.t.Fatal(err)
	}
	return out
}

// catalogLines answers the catalog's lines of frames, by FRAMEID, with
// how many lines each FRAMEID has.
func (w *world) catalogLines() (map[string]catalog.Line, map[string]int) {
	w.t.Helper()
	content, err := os.ReadFile(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		w.t.Fatal(err)
	}
	lines, counts := map[string]catalog.Line{}, map[string]int{}
	for raw := range strings.Lines(string(content)) {
		var line catalog.Line
		if json.Unmarshal([]byte(raw), &line) == nil {
			lines[line.FrameID] = line
			counts[line.FrameID]++
		}
	}
	return lines, counts
}
