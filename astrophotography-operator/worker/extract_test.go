package worker

import (
	"bytes"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

const centeringFrame = "east-main/east-tonight/_centering/0f3a.fits"

// nearTheField is a hint for a 400 mm tube and the simulator's 5.2 µm
// pixels, 2.68 arcseconds per pixel with a range of 10% each way,
// centered near where the fixture frame solves.
var nearTheField = Hint{Center: sky.Position{RA: 29.6, Dec: 59.8}, Radius: 5, ScaleLow: 2.41, ScaleHigh: 2.95}

// The fixture catalog has 1,675 stars, and astropy reads the median of
// their FWHM_IMAGE as 4.92813 pixels.
func TestExtractReturnsTheAXYAndTheStars(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	response, err := p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: nearTheField})
	if err != nil {
		t.Fatalf("extracting the frame: %v", err)
	}
	axy, _ := os.ReadFile(fixtureAXY)
	if !bytes.Equal(response.AXY, axy) || response.Stars != 1675 || math.Abs(response.MedianFWHM-4.92813) > 1e-4 {
		t.Errorf("the response has %d bytes of .axy, %d stars, and FWHM %v, want the fixture's .axy, 1675 stars, and FWHM 4.92813", len(response.AXY), response.Stars, response.MedianFWHM)
	}
}

// These command lines work with the real programs, so a change to an
// option must change this test too.
func TestEachProgramRunsWithItsOptions(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	if _, err := p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: nearTheField}); err != nil {
		t.Fatalf("extracting the frame: %v", err)
	}
	cases := []struct {
		program string
		want    string
	}{
		{"source-extractor", p.extractor.Outbox + "/" + centeringFrame +
			" -CATALOG_TYPE FITS_1.0 -CATALOG_NAME catalog.fits -PARAMETERS_NAME catalog.param -FILTER N -VERBOSE_TYPE QUIET"},
		{"solve-field", "--just-augment --overwrite --no-plots --no-remove-lines --uniformize 0" +
			" --x-column X_IMAGE --y-column Y_IMAGE --sort-column FLUX_AUTO --width 1280 --height 1024 --crpix-center" +
			" --scale-units arcsecperpix --scale-low 2.41 --scale-high 2.95 --cpulimit 60" +
			" --ra 29.6 --dec 59.8 --radius 5 -o field -D . catalog.fits"},
	}
	for _, c := range cases {
		runs := p.runs(t, c.program)
		if len(runs) != 1 || strings.Join(runs[0], " ") != c.want {
			t.Errorf("%s ran %q, want\n%s", c.program, runs, c.want)
		}
	}
}

// A radius of 0 asks for a blind solve, so the .axy has no position.
func TestABlindHintWritesNoPosition(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	blind := nearTheField
	blind.Radius = 0
	if _, err := p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: blind}); err != nil {
		t.Fatalf("extracting the frame: %v", err)
	}
	runs := p.runs(t, "solve-field")
	if len(runs) != 1 || slices.Contains(runs[0], "--ra") || slices.Contains(runs[0], "--radius") {
		t.Errorf("solve-field ran %q, want no position", runs)
	}
}

// A frame with no star cannot solve, so the worker returns no .axy and
// does not run solve-field.
func TestAFrameWithNoStarReturnsNoAXY(t *testing.T) {
	p := fakePrograms(t, programsFindNone)
	p.put(t, centeringFrame, simulatorFrame)
	response, err := p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: nearTheField})
	if err != nil || response.Stars != 0 || response.AXY != nil || len(p.runs(t, "solve-field")) != 0 {
		t.Errorf("the response is %+v with error %v and solve-field ran %d times, want no stars, no .axy, no error, and no run", response, err, len(p.runs(t, "solve-field")))
	}
}

func TestTheJobsFilesAreDeletedWhenItEnds(t *testing.T) {
	p := fakePrograms(t, programsWork)
	p.put(t, centeringFrame, simulatorFrame)
	_, _ = p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: nearTheField})
	if left, _ := os.ReadDir(p.extractor.Work); len(left) != 0 {
		t.Errorf("after the extraction the work directory holds %v, want nothing", left)
	}
}

// The worker reads only a path in the outbox's layout, so a request
// cannot read another file in the pod.
func TestARequestTheWorkerDoesNotRunIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		request ExtractRequest
	}{
		{"a path outside the outbox", ExtractRequest{Frame: "../../etc/passwd.fits", Hint: nearTheField}},
		{"a file that is not a frame", ExtractRequest{Frame: "east-main/notes.txt", Hint: nearTheField}},
		{"no scale", ExtractRequest{Frame: centeringFrame, Hint: Hint{Radius: 5}}},
		{"a scale range upside down", ExtractRequest{Frame: centeringFrame, Hint: Hint{ScaleLow: 3, ScaleHigh: 2}}},
		{"a radius below zero", ExtractRequest{Frame: centeringFrame, Hint: Hint{ScaleLow: 2, ScaleHigh: 3, Radius: -1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fakePrograms(t, programsWork)
			if _, err := p.extractor.Extract(t.Context(), c.request); !errors.Is(err, ErrBadRequest) {
				t.Errorf("Extract returned %v, want %v", err, ErrBadRequest)
			}
		})
	}
}

// The extractor refuses a frame whose path leaves the outbox even when
// the frame did not come from outbox.Parse, and reads a normal frame
// under the outbox.
func TestTheExtractorReadsOnlyAFrameUnderTheOutbox(t *testing.T) {
	cases := []struct {
		name  string
		frame outbox.Frame
		want  string
		err   error
	}{
		{"a frame in the outbox", outbox.Frame{Camera: "east-main", Reservation: "tonight", Name: "f1"}, "/outbox/east-main/tonight/_centering/f1.fits", nil},
		{"a camera that climbs above the outbox", outbox.Frame{Camera: "..", Reservation: "tonight", Name: "f1"}, "", ErrBadRequest},
		{"a name that climbs above the outbox", outbox.Frame{Camera: "east-main", Reservation: "tonight", Name: "../../../../etc/passwd"}, "", ErrBadRequest},
		{"an absolute camera", outbox.Frame{Camera: "/etc", Reservation: "tonight", Name: "f1"}, "", ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := framePath("/outbox", c.frame)
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("framePath = %q, %v, want %q, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestAFailedExtractionReportsTheProgramsOutput(t *testing.T) {
	cases := []struct {
		name     string
		behavior string
		frame    []byte
		work     string
		want     string
	}{
		{"a frame that is missing", programsWork, nil, "", "no such file"},
		{"a frame that is not FITS", programsWork, []byte("not FITS"), "", "FITS header"},
		{"a frame with no width", programsWork, frameHeader("SIMPLE  = T", "NAXIS   = 0"), "", "NAXIS1"},
		{"a frame with no height", programsWork, frameHeader("SIMPLE  = T", "NAXIS1  = 1280"), "", "NAXIS2"},
		{"a work directory that is gone", programsWork, simulatorFrame, "/gone", "gone"},
		{"a program that fails", programsFail, simulatorFrame, "", "cannot open the frame"},
		{"a catalog that is not FITS", programsWriteBad, simulatorFrame, "", "the catalog"},
		{"no catalog", programsWriteNothing, simulatorFrame, "", "catalog.fits"},
		{"a catalog with no FWHM_IMAGE", programsNoFWHM, simulatorFrame, "", "FWHM_IMAGE"},
		{"no .axy", programsSkipAXY, simulatorFrame, "", "field.axy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fakePrograms(t, c.behavior)
			p.extractor.Work += c.work
			if c.frame != nil {
				p.put(t, centeringFrame, c.frame)
			}
			_, err := p.extractor.Extract(t.Context(), ExtractRequest{Frame: centeringFrame, Hint: nearTheField})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Extract returned %v, want an error that contains %q", err, c.want)
			}
		})
	}
}

func TestMedianOfEachCount(t *testing.T) {
	cases := []struct {
		values []float64
		want   float64
	}{
		{nil, 0},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
	}
	for _, c := range cases {
		if got := median(c.values); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.values, got, c.want)
		}
	}
}

func TestTheFlagsOfTheWorkerMode(t *testing.T) {
	got, err := ParseOptions(nil, nil)
	want := Extractor{Outbox: "/outbox", Work: "/work", SourceExtractor: "/usr/bin/source-extractor", SolveField: "/usr/bin/solve-field"}
	if err != nil || got != want {
		t.Errorf("ParseOptions(nil) = %+v, %v, want %+v and no error", got, err, want)
	}
}

func TestTheWorkerModeRefusesWhatItDoesNotTake(t *testing.T) {
	cases := [][]string{{"--no-such-flag"}, {"extra"}}
	for _, args := range cases {
		if _, err := ParseOptions(args, &bytes.Buffer{}); err == nil {
			t.Errorf("ParseOptions(%q) returned no error, want one", args)
		}
	}
}
