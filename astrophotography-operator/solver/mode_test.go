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
			t.Errorf("%s: %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}
}

func TestTheSolverModeRefusesWhatItDoesNotTake(t *testing.T) {
	cases := [][]string{{"--no-such-flag"}, {"extra"}}
	for _, args := range cases {
		if _, err := ParseOptions(args, io.Discard); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
}

// startUntilCleanup starts the solver mode until the test ends. Its
// cleanup waits for the watch to stop, and it runs before the cleanup
// of every temporary directory that the test made before it. Without
// the wait, the watch can read the event of the index file that the
// directory's cleanup deletes, and write the configuration into the
// work directory while that cleanup empties it.
func startUntilCleanup(t *testing.T, o Options, logs io.Writer) *Server {
	t.Helper()
	s, err := Start(t.Context(), o, logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Error("the watch did not stop after the test ended")
		}
	})
	return s
}

// Start reads the claim, writes the engine's configuration into the
// work directory, and logs what it found.
func TestStartWritesTheConfigAndLogsTheSeries(t *testing.T) {
	indexes := writeFiles(t, map[string][]byte{"index-4208.fits": indexOfSeries(4208, arcmin30, arcmin42)})
	work := t.TempDir()
	var logs bytes.Buffer
	s := startUntilCleanup(t, Options{Indexes: indexes, Work: work, Engine: "astrometry-engine", CPULimit: 60}, &logs)
	config, _ := os.ReadFile(filepath.Join(work, "engine.cfg"))
	want := "index " + filepath.Join(indexes, "index-4208.fits") + "\ncpulimit 60\n"
	if series, _ := s.set.current(); string(config) != want || len(series) != 1 {
		t.Errorf("config %q with %d series, want %q with one", config, len(series), want)
	}
	if !strings.Contains(logs.String(), "index series 4208: 1 files, quads of 30 to 42 arcminutes") {
		t.Errorf("logged %q", logs.String())
	}
}

// Done closes after ctx ends and the watch of the index directory
// stops. A watch can read one more event after ctx ends and write the
// configuration again, so a caller waits on Done before it removes the
// work directory.
func TestDoneClosesWhenTheWatchStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	s, err := Start(ctx, Options{Indexes: t.TempDir(), Work: t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not stop after its context ended")
	}
}

// A claim with no index file still starts a server, so the operator
// reads an empty list and reports NoIndexFiles, and the pod does not
// restart again and again.
func TestStartWithNoIndexFilesServesAnEmptyList(t *testing.T) {
	var logs bytes.Buffer
	s := startUntilCleanup(t, Options{Indexes: t.TempDir(), Work: t.TempDir()}, &logs)
	if series, _ := s.set.current(); len(series) != 0 || !strings.Contains(logs.String(), "no index-*.fits") {
		t.Errorf("served %d series and logged %q", len(series), logs.String())
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
			t.Errorf("%s: no error", c.name)
		}
	}
}

// A frame as the sky shows it, with north up and east to the left, has
// a CD matrix with a negative determinant, which astrometry.net calls
// positive parity, and a rotation of 0.
func TestASolutionAsTheSkyShowsItHasPositiveParity(t *testing.T) {
	h := fits.Header{Cards: []fits.Card{
		{Key: "CRVAL1", Value: "83.8"}, {Key: "CRVAL2", Value: "-5.4"},
		{Key: "CD1_1", Value: "-0.000620"}, {Key: "CD1_2", Value: "0"},
		{Key: "CD2_1", Value: "0"}, {Key: "CD2_2", Value: "0.000620"},
	}}
	answer, err := answerOf(h)
	if err != nil || answer.Parity != ParityPositive || answer.Rotation != 0 || math.Abs(answer.Scale-2.232) > 1e-9 || answer.Index != 0 {
		t.Errorf("answered %+v, %v", answer, err)
	}
}

func TestAnIndexCommentThatDoesNotParseIsNoIndex(t *testing.T) {
	h := fits.Header{Cards: []fits.Card{{Key: "COMMENT", Value: "index id: many"}}}
	if got := indexOf(h); got != 0 {
		t.Errorf("index %d, want 0", got)
	}
}
