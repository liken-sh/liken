package solver

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

func TestTheFlagsOfTheSolverMode(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want Options
	}{
		{"the defaults", nil, Options{Indexes: "/indexes", Work: "/work", Engine: "/usr/bin/astrometry-engine", CPULimit: 60, Timeout: 2 * time.Minute}},
		{"a directory in the claim", []string{"--indexes", "/indexes/2mass"}, Options{Indexes: "/indexes/2mass", Work: "/work", Engine: "/usr/bin/astrometry-engine", CPULimit: 60, Timeout: 2 * time.Minute}},
	}
	for _, c := range cases {
		got, err := ParseOptions(c.args, io.Discard)
		if err != nil || got != c.want {
			t.Errorf("ParseOptions for %s = %+v, %v, want %+v and no error", c.name, got, err, c.want)
		}
	}
}

func TestTheSolverModeRefusesWhatItDoesNotTake(t *testing.T) {
	cases := [][]string{{"--no-such-flag"}, {"extra"}}
	for _, args := range cases {
		if _, err := ParseOptions(args, io.Discard); err == nil {
			t.Errorf("ParseOptions(%q) returned no error, want one", args)
		}
	}
}

// startUntilCleanup starts the solver mode, and its cleanup waits for
// the index watch to stop. Cleanups run in reverse order, so the wait
// happens before the test's temporary directories are deleted. Without
// it, the watch can see an index file deleted by that cleanup and write
// the configuration into a work directory that is being emptied.
func startUntilCleanup(t *testing.T, o Options, logs io.Writer) *Server {
	t.Helper()
	s, err := Start(t.Context(), o, logs)
	if err != nil {
		t.Fatalf("starting the solver mode: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Error("the index watch is still running 5 seconds after the test ended, want it stopped")
		}
	})
	return s
}

func TestStartWritesTheConfigAndLogsTheSeries(t *testing.T) {
	indexes := writeFiles(t, map[string][]byte{"index-4208.fits": indexOfSeries(4208, arcmin30, arcmin42)})
	work := t.TempDir()
	var logs bytes.Buffer
	s := startUntilCleanup(t, Options{Indexes: indexes, Work: work, Engine: "astrometry-engine", CPULimit: 60}, &logs)
	config, _ := os.ReadFile(filepath.Join(work, "engine.cfg"))
	want := "index " + filepath.Join(indexes, "index-4208.fits") + "\ncpulimit 60\n"
	if series, _ := s.set.current(); string(config) != want || len(series) != 1 {
		t.Errorf("the engine configuration is %q with %d series, want %q with 1 series", config, len(series), want)
	}
	if !strings.Contains(logs.String(), "index series 4208: 1 files, quads of 30 to 42 arcminutes") {
		t.Errorf("the log is %q, want the line about index series 4208", logs.String())
	}
}

func TestDoneClosesWhenTheWatchStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	s, err := Start(ctx, Options{Indexes: t.TempDir(), Work: t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatalf("starting the solver mode: %v", err)
	}
	cancel()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the index watch is still running 5 seconds after its context ended, want it stopped")
	}
}

func TestStartWithNoIndexFilesServesAnEmptyList(t *testing.T) {
	var logs bytes.Buffer
	s := startUntilCleanup(t, Options{Indexes: t.TempDir(), Work: t.TempDir()}, &logs)
	if series, _ := s.set.current(); len(series) != 0 || !strings.Contains(logs.String(), "no index-*.fits") {
		t.Errorf("the server lists %d series and the log is %q, want 0 series and a line about no index-*.fits files", len(series), logs.String())
	}
}

func TestStartFails(t *testing.T) {
	cases := []struct {
		name    string
		options Options
	}{
		{"a work directory that is gone", Options{Indexes: t.TempDir(), Work: filepath.Join(t.TempDir(), "gone")}},
		{"a work directory that is gone, with no index directory", Options{Indexes: filepath.Join(t.TempDir(), "gone"), Work: filepath.Join(t.TempDir(), "gone")}},
	}
	for _, c := range cases {
		if _, err := Start(t.Context(), c.options, io.Discard); err == nil {
			t.Errorf("Start with %s returned no error, want one", c.name)
		}
	}
}

// A frame with north up and east to the left, as the sky appears, has a
// CD matrix with a negative determinant. astrometry.net calls that
// positive parity. Its rotation is 0.
func TestASolutionAsTheSkyShowsItHasPositiveParity(t *testing.T) {
	h := fits.Header{Cards: []fits.Card{
		{Key: "CRVAL1", Value: "83.8"}, {Key: "CRVAL2", Value: "-5.4"},
		{Key: "CD1_1", Value: "-0.000620"}, {Key: "CD1_2", Value: "0"},
		{Key: "CD2_1", Value: "0"}, {Key: "CD2_2", Value: "0.000620"},
	}}
	solution, err := solutionOf(h)
	if err != nil || solution.Parity != ParityPositive || solution.Rotation != 0 || math.Abs(solution.Scale-2.232) > 1e-9 || solution.Index != 0 {
		t.Errorf("solutionOf = %+v, %v, want positive parity, rotation 0, scale 2.232, index 0, and no error", solution, err)
	}
}

func TestAnIndexCommentThatDoesNotParseIsNoIndex(t *testing.T) {
	h := fits.Header{Cards: []fits.Card{{Key: "COMMENT", Value: "index id: many"}}}
	if got := indexOf(h); got != 0 {
		t.Errorf("indexOf = %d, want 0", got)
	}
}
