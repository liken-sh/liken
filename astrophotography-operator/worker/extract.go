package worker

// Extraction runs two programs on a frame. SourceExtractor finds the
// stars and writes a catalog. solve-field --just-augment turns the
// catalog and the hint into an .axy file, which the operator sends to
// the solver.

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

// catalogColumns are the columns SourceExtractor writes. solve-field
// reads the positions and sorts by FLUX_AUTO, and measure reads
// FWHM_IMAGE. No code reads ELONGATION and FLAGS. They are in the
// catalog for grading frames.
var catalogColumns = []string{"X_IMAGE", "Y_IMAGE", "FLUX_AUTO", "FWHM_IMAGE", "ELONGATION", "FLAGS"}

// cpuLimit is the ANCLIM header of each .axy, in seconds, so a field
// that cannot solve does not occupy the solver for long. The solver's
// engine also caps it at its own limit.
const cpuLimit = 60

// The files of one job, in the job's own directory. solve-field runs
// with -D . and -o field, so it writes field.axy there.
const (
	parametersFile = "catalog.param"
	catalogFile    = "catalog.fits"
	fieldName      = "field"
)

// ErrBadRequest marks a request that the worker refuses before it runs
// a program.
var ErrBadRequest = errors.New("the worker does not accept the request")

// Extractor runs SourceExtractor and solve-field on the frames of one
// outbox.
type Extractor struct {
	// Outbox is the directory where the pod mounts the outbox claim,
	// outbox.Root.
	Outbox string
	// Work is a writable directory for each job's files, an emptyDir.
	Work string
	// SourceExtractor and SolveField are the paths of the programs.
	SourceExtractor string
	SolveField      string
}

// Extract finds the stars of one frame and returns the .axy, with the
// number of stars and their median FWHM. For a frame with no star it
// returns an empty ExtractResponse and no error, and does not run
// solve-field.
func (e Extractor) Extract(ctx context.Context, request ExtractRequest) (ExtractResponse, error) {
	frame, err := outbox.Parse(request.Frame)
	if err != nil {
		return ExtractResponse{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if err := checkHint(request.Hint); err != nil {
		return ExtractResponse{}, err
	}
	path, err := framePath(e.Outbox, frame)
	if err != nil {
		return ExtractResponse{}, err
	}
	width, height, err := frameSize(path)
	if err != nil {
		return ExtractResponse{}, err
	}
	dir, err := os.MkdirTemp(e.Work, "job-")
	if err != nil {
		return ExtractResponse{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, parametersFile), []byte(columnList()), 0o600); err != nil {
		return ExtractResponse{}, err
	}
	if err := run(ctx, dir, e.SourceExtractor, sourceExtractorArgs(path)); err != nil {
		return ExtractResponse{}, err
	}
	stars, fwhm, err := measure(filepath.Join(dir, catalogFile))
	if err != nil || stars == 0 {
		return ExtractResponse{}, err
	}
	if err := run(ctx, dir, e.SolveField, augmentArgs(width, height, request.Hint)); err != nil {
		return ExtractResponse{}, err
	}
	axy, err := os.ReadFile(filepath.Join(dir, fieldName+".axy"))
	if err != nil {
		return ExtractResponse{}, err
	}
	return ExtractResponse{AXY: axy, Stars: stars, MedianFWHM: fwhm}, nil
}

// framePath refuses a frame whose path is not under the outbox. The
// frame comes from a request, and this check does not depend on
// outbox.Parse, so a change to the parser cannot let a request read a
// file outside the outbox.
func framePath(outboxRoot string, frame outbox.Frame) (string, error) {
	relative := frame.Path()
	if !filepath.IsLocal(relative) {
		return "", fmt.Errorf("%w: the frame %s is not under the outbox", ErrBadRequest, relative)
	}
	return filepath.Join(outboxRoot, relative), nil
}

// checkHint refuses a hint that solve-field would reject or misread.
func checkHint(h Hint) error {
	switch {
	case h.ScaleLow <= 0 || h.ScaleHigh < h.ScaleLow:
		return fmt.Errorf("%w: the scale range %g to %g arcseconds per pixel must start above 0 and must not end below its start", ErrBadRequest, h.ScaleLow, h.ScaleHigh)
	case h.Radius < 0:
		return fmt.Errorf("%w: the radius %g is below 0", ErrBadRequest, h.Radius)
	}
	return nil
}

// frameSize reads a frame's width and height from its FITS header.
// solve-field needs them because the catalog has only star positions.
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

// -FILTER N turns off convolution, so SourceExtractor detects stars on
// the pixels as the camera wrote them and needs no kernel file.
func sourceExtractorArgs(frame string) []string {
	return []string{frame,
		"-CATALOG_TYPE", "FITS_1.0", "-CATALOG_NAME", catalogFile,
		"-PARAMETERS_NAME", parametersFile, "-FILTER", "N", "-VERBOSE_TYPE", "QUIET",
	}
}

// augmentArgs returns the command line of solve-field. Without
// --no-remove-lines and --uniformize 0, solve-field runs two Python
// modules, and the image has no Python. --crpix-center puts the
// solution's reference point at the center of the frame. A hint with
// radius 0 adds no position, so the solver searches the whole sky.
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

// run runs one program in the job's directory. Its error includes the
// program's output.
func run(ctx context.Context, dir, program string, args []string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("running %s: %w: %s", filepath.Base(program), err, bytes.TrimSpace(output))
	}
	return nil
}

// measure returns the number of stars in the catalog and the median of
// their FWHM_IMAGE in pixels, which is 0 when there is no star.
func measure(catalog string) (int32, float64, error) {
	f, err := os.Open(catalog)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	table, err := fits.ReadTable(f)
	if err != nil {
		return 0, 0, fmt.Errorf("reading the catalog: %w", err)
	}
	fwhm, err := table.Column("FWHM_IMAGE")
	if err != nil {
		return 0, 0, fmt.Errorf("reading the FWHM_IMAGE column of the catalog: %w", err)
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
