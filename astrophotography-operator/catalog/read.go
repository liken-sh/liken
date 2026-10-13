package catalog

// The reader of a catalog. The promotion Job reads the whole catalog
// before each run, to find the frames that an earlier run recorded, and
// reports the totals of the whole file after it. The totals come from
// the file and not from what one run did, so the archived counts in a
// target's status survive a restart of the operator, a report that the
// operator never read, and a status that a person cleared.

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
)

// Total counts the catalog's frames of one combination of settings.
type Total struct {
	Settings
	Frames int32 `json:"frames"`
}

// Catalog is what a reader learned from one catalog file.
type Catalog struct {
	frames map[string]Line
	// order holds each FRAMEID in the order of its first line.
	order []string
}

// maxLine bounds one line. A line of the frame record takes about 500
// bytes, and a later record keyed by FRAMEID is of the same size.
const maxLine = 1 << 20

// Read reads a whole catalog. A line that does not decode is skipped:
// a crash during an append can leave the last line cut short, and the
// next append starts a new line after it (Append). A line of a record
// other than RecordFrame is skipped too, so a reader of this version
// reads a catalog that a later version extended. A FRAMEID recorded
// twice counts once, by its first line.
func Read(r io.Reader) (*Catalog, error) {
	c := &Catalog{frames: map[string]Line{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxLine)
	for scanner.Scan() {
		var line Line
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Record != RecordFrame || line.FrameID == "" {
			continue
		}
		c.Add(line)
	}
	return c, scanner.Err()
}

// ReadFile reads the catalog at a path. A catalog that does not exist
// yet is empty: no frame of the target reached the archive.
func ReadFile(name string) (*Catalog, error) {
	file, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return &Catalog{frames: map[string]Line{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Read(file)
}

// Add records a frame's line, unless the catalog holds its FRAMEID.
func (c *Catalog) Add(line Line) {
	if _, held := c.frames[line.FrameID]; held {
		return
	}
	c.frames[line.FrameID] = line
	c.order = append(c.order, line.FrameID)
}

// Frame answers the line of a frame by its FRAMEID.
func (c *Catalog) Frame(id string) (Line, bool) {
	line, found := c.frames[id]
	return line, found
}

// Totals counts the frames by their settings, sorted by filter,
// exposure, binning, gain, and offset, with an absent gain or offset
// before any value.
func (c *Catalog) Totals() []Total {
	var totals []Total
	for _, id := range c.order {
		settings := c.frames[id].Settings
		index := slices.IndexFunc(totals, func(t Total) bool { return t.Settings.Equal(settings) })
		if index < 0 {
			totals = append(totals, Total{Settings: settings})
			index = len(totals) - 1
		}
		totals[index].Frames++
	}
	slices.SortFunc(totals, func(a, b Total) int { return a.Settings.compare(b.Settings) })
	return totals
}

// Equal reports whether two frames have the same settings.
func (s Settings) Equal(other Settings) bool {
	return s.compare(other) == 0
}

func (s Settings) compare(other Settings) int {
	return cmp.Or(
		cmp.Compare(s.Filter, other.Filter),
		cmp.Compare(s.ExposureSeconds, other.ExposureSeconds),
		cmp.Compare(s.Binning, other.Binning),
		compareOptional(s.Gain, other.Gain),
		compareOptional(s.Offset, other.Offset),
	)
}

// compareOptional orders an absent value before any value.
func compareOptional(a, b *float64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return cmp.Compare(*a, *b)
}
