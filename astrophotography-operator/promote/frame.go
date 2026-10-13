package promote

// What the promotion Job reads from a frame's header: its identity, the
// settings that decide which sequence it counts toward, the start of
// the exposure, and whether the file is whole.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// incomplete is a frame shorter than its header and data: the driver
// is still writing it, or never finished. Its text says how much of
// the file is there.
type incomplete string

func (i incomplete) Error() string { return string(i) }

// dateObs is how the driver writes DATE-OBS: UTC with milliseconds and
// no zone, such as 2026-10-12T20:08:26.864.
const dateObs = "2006-01-02T15:04:05.999999999"

// startTime is how a catalog line writes the start of an exposure.
const startTime = "2006-01-02T15:04:05.000Z07:00"

// readFrame reads a frame's header into the fields of its catalog line.
// A file that ends before its header and data do answers an
// incomplete.
func readFrame(name string) (catalog.Line, error) {
	file, err := os.Open(name)
	if err != nil {
		return catalog.Line{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return catalog.Line{}, err
	}
	h, err := readHeader(file)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return catalog.Line{}, incomplete("the file ends inside its header")
	}
	if err != nil {
		return catalog.Line{}, err
	}
	whole, err := h.fileSize()
	if err != nil {
		return catalog.Line{}, err
	}
	if info.Size() < whole {
		return catalog.Line{}, incomplete(fmt.Sprintf("the file holds %d of %d bytes", info.Size(), whole))
	}
	return lineOf(h), nil
}

// lineOf answers the fields of a catalog line that the header gives. A
// header with no XBINNING took the camera's binning of 1.
func lineOf(h header) catalog.Line {
	line := catalog.Line{Record: catalog.RecordFrame}
	line.FrameID, _ = h.text("FRAMEID")
	line.Filter, _ = h.text("FILTER")
	line.ExposureSeconds, _ = h.number("EXPTIME")
	line.Binning = 1
	if binning, ok := h.number("XBINNING"); ok {
		line.Binning = int32(binning)
	}
	if gain, ok := h.number("GAIN"); ok {
		line.Gain = &gain
	}
	if offset, ok := h.number("OFFSET"); ok {
		line.Offset = &offset
	}
	if text, ok := h.text("DATE-OBS"); ok {
		if start, err := time.Parse(dateObs, text); err == nil {
			line.StartTime = start.UTC().Format(startTime)
		}
	}
	return line
}
