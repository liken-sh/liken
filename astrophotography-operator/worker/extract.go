package worker

// The extraction: SourceExtractor finds the stars in a frame and writes
// a catalog, and solve-field --just-augment turns the catalog into an
// .axy with the hints, which the operator carries to the solver. Plan
// 02 measured both on a frame of the CCD simulator: 0.26 s and 0.25 s
// in their own containers, with the containers' start.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
)

// The columns of SourceExtractor's catalog. solve-field reads the
// position and sorts by the flux, and the size, the shape, and the
// flags are there for the grading of plan 06.
var catalogColumns = []string{"X_IMAGE", "Y_IMAGE", "FLUX_AUTO", "FWHM_IMAGE", "ELONGATION", "FLAGS"}

// cpuLimit is the ANCLIM of each .axy, in seconds, from plan 02, so a
// field that cannot solve does not hold the solver's queue. The solver
// applies its own limit too.
const cpuLimit = 60

// The files of a job, in its own directory. solve-field names each
// output after field, relative to the directory, because it runs with
// -D ., so the solver finds them in the directory where it writes the
// .axy.
const (
	parametersFile = "catalog.param"
	catalogFile    = "catalog.fits"
	fieldName      = "field"
)

// ErrBadRequest is the error of a request that the worker refuses
// before it runs a program.
var ErrBadRequest = errors.New("the request is not one the worker runs")

// Extractor runs the two programs on the frames of one outbox.
type Extractor struct {
	// Outbox is where the pod mounts the outbox claim, outbox.Root.
	Outbox string
	// Work is a writable directory for each job's files, an emptyDir.
	Work string
	// SourceExtractor and SolveField are the programs' paths.
	SourceExtractor string
	SolveField      string
}

// Extract finds the stars of one frame and answers the .axy, with the
// count and median size of the stars.
func (e Extractor) Extract(ctx context.Context, request ExtractRequest) (ExtractAnswer, error) {
	frame, err := outbox.Parse(request.Frame)
	if err != nil {
		return ExtractAnswer{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if err := checkHint(request.Hint); err != nil {
		return ExtractAnswer{}, err
	}
	path := filepath.Join(e.Outbox, frame.Path())
	width, height, err := frameSize(path)
	if err != nil {
		return ExtractAnswer{}, err
	}
	dir, err := os.MkdirTemp(e.Work, "job-")
	if err != nil {
		return ExtractAnswer{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, parametersFile), []byte(columnList()), 0o600); err != nil {
		return ExtractAnswer{}, err
	}
	if err := run(ctx, dir, e.SourceExtractor, sourceExtractorArgs(path)); err != nil {
		return ExtractAnswer{}, err
	}
	stars, fwhm, err := measure(filepath.Join(dir, catalogFile))
	if err != nil || stars == 0 {
		return ExtractAnswer{}, err
	}
	if err := run(ctx, dir, e.SolveField, augmentArgs(width, height, request.Hint)); err != nil {
		return ExtractAnswer{}, err
	}
	axy, err := os.ReadFile(filepath.Join(dir, fieldName+".axy"))
	if err != nil {
		return ExtractAnswer{}, err
	}
	return ExtractAnswer{AXY: axy, Stars: stars, MedianFWHM: fwhm}, nil
}

// checkHint refuses a hint that solve-field would refuse or misread: a
// scale range that is not a range, and a radius below zero.
func checkHint(h Hint) error {
	switch {
	case h.ScaleLow <= 0 || h.ScaleHigh < h.ScaleLow:
		return fmt.Errorf("%w: the scale %g to %g is not a range of arcseconds per pixel", ErrBadRequest, h.ScaleLow, h.ScaleHigh)
	case h.Radius < 0:
		return fmt.Errorf("%w: the radius %g is below zero", ErrBadRequest, h.Radius)
	}
	return nil
}

// frameSize reads a frame's width and height from its header, which
// solve-field needs because a catalog holds only the stars.
func frameSize(path string) (width, height int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	h, err := fits.ReadHeader(f)
	if err != nil {
		return 0, 0, err
	}
	if width, err = h.Int("NAXIS1"); err != nil {
		return 0, 0, err
	}
	if height, err = h.Int("NAXIS2"); err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func columnList() string {
	var b bytes.Buffer
	for _, column := range catalogColumns {
		b.WriteString(column + "\n")
	}
	return b.String()
}

// sourceExtractorArgs are the options of the image's smoke check, which
// proved them on Debian's SourceExtractor. With no filter, it reads
// each pixel as the camera wrote it, and it needs no file of the
// convolution kernel.
func sourceExtractorArgs(frame string) []string {
	return []string{frame,
		"-CATALOG_TYPE", "FITS_1.0", "-CATALOG_NAME", catalogFile,
		"-PARAMETERS_NAME", parametersFile, "-FILTER", "N", "-VERBOSE_TYPE", "QUIET",
	}
}

// augmentArgs are the options of plan 02. Without --no-remove-lines
// and --uniformize 0, solve-field runs two Python modules, and the
// image holds no Python. --crpix-center makes the solution's reference
// point the center of the frame. A radius of 0 gives no position, so
// the solver searches the whole sky.
func augmentArgs(width, height int64, h Hint) []string {
	args := []string{"--just-augment", "--overwrite", "--no-plots",
		"--no-remove-lines", "--uniformize", "0",
		"--x-column", "X_IMAGE", "--y-column", "Y_IMAGE", "--sort-column", "FLUX_AUTO",
		"--width", strconv.FormatInt(width, 10), "--height", strconv.FormatInt(height, 10),
		"--crpix-center",
		"--scale-units", "arcsecperpix", "--scale-low", number(h.ScaleLow), "--scale-high", number(h.ScaleHigh),
		"--cpulimit", strconv.Itoa(cpuLimit),
	}
	if h.Radius > 0 {
		args = append(args, "--ra", number(h.Center.RA), "--dec", number(h.Center.Dec), "--radius", number(h.Radius))
	}
	return append(args, "-o", fieldName, "-D", ".", catalogFile)
}

func number(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// run runs one program in the job's directory, and answers its output
// with the error when it fails.
func run(ctx context.Context, dir, program string, args []string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(program), err, bytes.TrimSpace(output))
	}
	return nil
}

// measure counts the catalog's stars and answers the median of their
// FWHM_IMAGE, in pixels, or 0 when it holds no star.
func measure(catalog string) (int32, float64, error) {
	f, err := os.Open(catalog)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	table, err := fits.ReadTable(f)
	if err != nil {
		return 0, 0, fmt.Errorf("the catalog: %w", err)
	}
	fwhm, err := table.Column("FWHM_IMAGE")
	if err != nil {
		return 0, 0, fmt.Errorf("the catalog: %w", err)
	}
	return int32(len(fwhm)), median(fwhm), nil
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}
