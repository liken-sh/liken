package solver

// CI has no astrometry-engine, so the tests run a fake one: this test
// binary itself, started again with FAKE_ENGINE set. It reads the .axy
// that it is given the way the engine does, and writes the files the
// probe of plan 02 recorded: the WCS file and the .solved file that
// the .axy names when the field solves, and nothing when it does not.
// The WCS file is a real one, which the engine wrote for the same
// frame as the fixture .axy.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

// The fake's behaviors, by the value of FAKE_ENGINE.
const (
	engineSolves = "solves"
	engineMisses = "misses"
	engineFails  = "fails"
	engineHangs  = "hangs"
	// The two that write a WCS file the solver cannot read.
	engineWritesADirectory = "directory"
	engineWritesGarbage    = "garbage"
)

// The fixtures of the fits package: an .axy that the worker's options
// made of a frame of the CCD simulator, and the engine's solution.
const (
	fixtureAXY = "../fits/testdata/axy.fits"
	fixtureWCS = "../fits/testdata/wcs.fits"
)

func TestMain(m *testing.M) {
	if behavior := os.Getenv("FAKE_ENGINE"); behavior != "" {
		os.Exit(fakeEngine(behavior, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeEngine takes the engine's arguments, -c <config> <axy>, and runs
// in the job's directory.
func fakeEngine(behavior string, args []string) int {
	if len(args) != 3 || args[0] != "-c" {
		fmt.Fprintf(os.Stderr, "usage: astrometry-engine -c <config> <axy>, not %q\n", args)
		return 2
	}
	if _, err := os.Stat(args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := os.Open(args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	h, err := fits.ReadHeader(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	wcs, _ := h.String("ANWCS")
	switch behavior {
	case engineFails:
		// The engine logs a line for each index file before it fails, so
		// the error keeps only the end of the log.
		fmt.Fprintln(os.Stderr, strings.Repeat("Loading index file\n", 50)+"Failed to load index file")
		return 1
	case engineWritesADirectory:
		_ = os.Mkdir(wcs, 0o700)
		return 0
	case engineWritesGarbage:
		_ = os.WriteFile(wcs, []byte("not FITS"), 0o600)
		return 0
	case engineHangs:
		time.Sleep(time.Minute)
		return 0
	case engineMisses:
		return 0
	}
	solved, _ := h.String("ANSOLVED")
	solution, err := os.ReadFile(os.Getenv("FAKE_ENGINE_WCS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_ = os.WriteFile(solved, []byte{1}, 0o600)
	if err := os.WriteFile(wcs, solution, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

// fakeEngineAt answers an Engine that runs the fake with one behavior,
// with a configuration file and a work directory of its own.
func fakeEngineAt(t *testing.T, behavior string) Engine {
	t.Helper()
	wcs, err := filepath.Abs(fixtureWCS)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ENGINE", behavior)
	t.Setenv("FAKE_ENGINE_WCS", wcs)
	work := t.TempDir()
	config := filepath.Join(work, "engine.cfg")
	if err := os.WriteFile(config, []byte("index /indexes/index-4207-00.fits\ncpulimit 60\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Engine{Program: os.Args[0], Config: config, Work: work, Timeout: 10 * time.Second}
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
