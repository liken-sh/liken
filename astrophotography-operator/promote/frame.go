package promote

// Reads a frame's FITS header for the promotion Job: the frame's
// identity, the settings that decide which sequence it belongs to, the
// start time of the exposure, and whether the whole file is there.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// incomplete is the error for a frame file that is shorter than its
// header and data, because the driver is still writing it or never
// finished.
type incomplete string

func (i incomplete) Error() string { return string(i) }

// dateObs is the layout of DATE-OBS in the driver's frames: UTC with
// milliseconds and no zone, such as 2026-10-12T20:08:26.864.
const dateObs = "2006-01-02T15:04:05.999999999"

// startTime is the layout of a catalog line's exposure start time.
const startTime = "2006-01-02T15:04:05.000Z07:00"

// readFrame reads a frame's header into a catalog line. It returns
// incomplete when the file ends before its header or data do.
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

// lineOf returns the catalog line fields that the header holds. A
// header with no XBINNING gives a binning of 1.
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
