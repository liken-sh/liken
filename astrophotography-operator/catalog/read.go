package catalog

// The promotion Job reads the whole catalog before each run, to find
// the frames that an earlier run already recorded, and reports the
// totals of the whole file when the run ends. Because the totals come
// from the file and not from one run, the archived counts in a target's
// status become correct again after the operator restarts, after a
// report that the operator never read, and after a person clears the
// status.

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

// Total is the number of frames in the catalog with one set of
// settings.
type Total struct {
	Settings
	Frames int32 `json:"frames"`
}

// Catalog is the frames read from one catalog file.
type Catalog struct {
	frames map[string]Line
	// order lists each FRAMEID in the order of its first line.
	order []string
}

// maxLine is the longest line that Read accepts. A longer line makes
// Read fail. A frame line has a fixed set of short fields, so it is far
// below this limit.
const maxLine = 1 << 20

// Read reads a whole catalog. It skips a line that is not valid JSON,
// because a crash during an append can leave a cut-off line in the
// middle of the file. It also skips a line whose record is not
// RecordFrame, so this version can read a catalog that a later version
// added other records to. When two lines have the same FRAMEID, the
// first one counts.
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

// ReadFile reads the catalog at a path. A catalog file that does not
// exist is an empty catalog, because no frame of the target has reached
// the archive yet.
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

// Add adds a frame's line, unless the catalog already has a line with
// its FRAMEID.
func (c *Catalog) Add(line Line) {
	if _, found := c.frames[line.FrameID]; found {
		return
	}
	c.frames[line.FrameID] = line
	c.order = append(c.order, line.FrameID)
}

// Frame returns the line with a FRAMEID, and false when there is none.
func (c *Catalog) Frame(id string) (Line, bool) {
	line, found := c.frames[id]
	return line, found
}

// Totals counts the frames for each set of settings, sorted by filter,
// exposure, binning, gain, and offset. A missing gain or offset sorts
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
