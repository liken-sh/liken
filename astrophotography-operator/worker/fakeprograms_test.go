package worker

// CI has neither source-extractor nor solve-field, so the tests run
// this test binary in their place, through a symlink named after each
// program. With FAKE_WORKER set, TestMain runs the fake program instead
// of the tests. Each fake appends its arguments to a log, checks the
// options that the real program needs, and writes the file that the
// real program would write, copied from the fixtures of the fits
// package: a catalog and an .axy of a frame from the INDI CCD
// simulator.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

// The behaviors of the fakes, selected by the value of FAKE_WORKER.
const (
	programsWork     = "work"
	programsFindNone = "none"
	programsFail     = "fail"
	programsWriteBad = "bad"
	// Both programs exit 0 and write nothing.
	programsWriteNothing = "nothing"
	// solve-field exits 0 and writes no .axy.
	programsSkipAXY = "skip"
	// The catalog has no FWHM_IMAGE column.
	programsNoFWHM = "nofwhm"
)

const (
	fixtureCatalog = "../fits/testdata/catalog.fits"
	fixtureAXY     = "../fits/testdata/axy.fits"
)

func TestMain(m *testing.M) {
	if behavior := os.Getenv("FAKE_WORKER"); behavior != "" {
		os.Exit(fakeProgram(filepath.Base(os.Args[0]), behavior, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeProgram(program, behavior string, args []string) int {
	log, _ := os.OpenFile(os.Getenv("FAKE_WORKER_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintf(log, "%s %s\n", program, strings.Join(args, " "))
	_ = log.Close()
	switch behavior {
	case programsFail:
		fmt.Fprintf(os.Stderr, "%s: cannot open the frame\n", program)
		return 1
	case programsWriteNothing:
		return 0
	}
	fixtures := os.Getenv("FAKE_WORKER_FIXTURES")
	switch program {
	case "source-extractor":
		return fakeSourceExtractor(behavior, fixtures, args)
	case "solve-field":
		return fakeSolveField(behavior, fixtures, args)
	}
	return 2
}

// fakeSourceExtractor reads the list of output columns from its
// parameters file, as the real program does, and writes the fixture
// catalog, or a catalog with no rows.
func fakeSourceExtractor(behavior, fixtures string, args []string) int {
	parameters, err := os.ReadFile(option(args, "-PARAMETERS_NAME"))
	if err != nil || strings.Fields(string(parameters))[3] != "FWHM_IMAGE" {
		fmt.Fprintf(os.Stderr, "the parameters %q: %v\n", parameters, err)
		return 2
	}
	catalog, _ := os.ReadFile(filepath.Join(fixtures, "catalog.fits"))
	switch behavior {
	case programsFindNone:
		catalog = emptied(catalog)
	case programsWriteBad:
		catalog = []byte("not FITS")
	case programsNoFWHM:
		catalog = bytes.Replace(catalog, []byte("'FWHM_IMAGE'"), []byte("'FWHM_OTHER'"), 1)
	}
	return written(option(args, "-CATALOG_NAME"), catalog)
}

// fakeSolveField fails unless it gets the two options that stop the
// real program from running Python. It writes the fixture .axy to
// <-D>/<-o>.axy.
func fakeSolveField(behavior, fixtures string, args []string) int {
	if !slices.Contains(args, "--no-remove-lines") || option(args, "--uniformize") != "0" {
		fmt.Fprintln(os.Stderr, "/usr/bin/python3: not found")
		return 1
	}
	if behavior == programsSkipAXY {
		return 0
	}
	axy, _ := os.ReadFile(filepath.Join(fixtures, "axy.fits"))
	return written(filepath.Join(option(args, "-D"), option(args, "-o")+".axy"), axy)
}

func option(args []string, name string) string {
	at := slices.Index(args, name)
	if at < 0 || at+1 >= len(args) {
		return ""
	}
	return args[at+1]
}

func written(path string, data []byte) int {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

// emptied returns a copy of the catalog with the same header and no
// rows, like the catalog of a frame with no stars.
func emptied(catalog []byte) []byte {
	table := headerEnd(catalog, 0)
	rows := headerEnd(catalog, table)
	out := bytes.Clone(catalog[:rows])
	for at := table; at < rows; at += 80 {
		if strings.HasPrefix(string(out[at:]), "NAXIS2  =") {
			copy(out[at:at+80], fmt.Sprintf("%-80s", "NAXIS2  =                    0"))
		}
	}
	return out
}

// headerEnd returns the offset after the FITS block that contains the
// END card of the header that starts at from.
func headerEnd(data []byte, from int) int {
	for at := from; at+80 <= len(data); at += 80 {
		if strings.TrimRight(string(data[at:at+80]), " ") == "END" {
			return (at/fits.BlockSize + 1) * fits.BlockSize
		}
	}
	return len(data)
}

// programs holds the Extractor that runs the fake programs of one test,
// and the log of their runs.
type programs struct {
	extractor Extractor
	// log is the path of a file with one line per run: the program and
	// its arguments.
	log string
}

// fakePrograms links both programs to this test binary in a new
// directory, sets their behavior, and returns an Extractor that uses
// them with a new outbox and work directory.
func fakePrograms(t *testing.T, behavior string) programs {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"source-extractor", "solve-field"} {
		if err := os.Symlink(os.Args[0], filepath.Join(bin, name)); err != nil {
			t.Fatalf("linking %s to the test binary: %v", name, err)
		}
	}
	fixtures, err := filepath.Abs("../fits/testdata")
	if err != nil {
		t.Fatalf("resolving the fixture directory: %v", err)
	}
	log := filepath.Join(t.TempDir(), "runs.log")
	t.Setenv("FAKE_WORKER", behavior)
	t.Setenv("FAKE_WORKER_FIXTURES", fixtures)
	t.Setenv("FAKE_WORKER_LOG", log)
	return programs{
		extractor: Extractor{
			Outbox:          t.TempDir(),
			Work:            t.TempDir(),
			SourceExtractor: filepath.Join(bin, "source-extractor"),
			SolveField:      filepath.Join(bin, "solve-field"),
		},
		log: log,
	}
}

// runs returns the arguments of each run of a program, in order.
func (p programs) runs(t *testing.T, program string) [][]string {
	t.Helper()
	data, _ := os.ReadFile(p.log)
	var out [][]string
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if fields[0] == program {
			out = append(out, fields[1:])
		}
	}
	return out
}

// frameHeader builds a FITS header from cards, padded to one block,
// with no pixel data, because the fakes never read pixels.
func frameHeader(cards ...string) []byte {
	var b bytes.Buffer
	for _, c := range append(cards, "END") {
		fmt.Fprintf(&b, "%-80s", c)
	}
	b.Write(bytes.Repeat([]byte(" "), fits.BlockSize-b.Len()))
	return b.Bytes()
}

var simulatorFrame = frameHeader("SIMPLE  = T", "BITPIX  = 16", "NAXIS   = 2", "NAXIS1  = 1280", "NAXIS2  = 1024")

// put writes a file into the outbox, with the directories it needs.
func (p programs) put(t *testing.T, path string, data []byte) {
	t.Helper()
	full := filepath.Join(p.extractor.Outbox, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
