package solver

// These tests run the real inotify on a directory in t.TempDir(), so
// they run outside a synctest bubble: a goroutine that waits on a file
// descriptor is not durably blocked, and a bubble would never settle.
// Each wait for the index set is bounded, so a watch that misses an
// event fails the test in seconds.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

var (
	series4207 = astrophotography.IndexSeries{ID: 4207, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}}
	series4208 = astrophotography.IndexSeries{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}}
)

// startWatching starts the solver mode on a directory of index files,
// with its own work directory, until the test ends.
func startWatching(t *testing.T, indexes string) (*Server, string) {
	t.Helper()
	work := t.TempDir()
	s, err := Start(t.Context(), Options{Indexes: indexes, Work: work, CPULimit: 60}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return s, work
}

// waitForSeries waits until the server serves the series, and fails
// the test when it does not within a few seconds.
func waitForSeries(t *testing.T, s *Server, want Indexes) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		got, changed := s.set.current()
		if slices.Equal(got, want) {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("the solver serves %+v, want %+v", got, want)
		}
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Job that downloads index files into the claim while the solver
// runs adds each file to the set when it closes the file, and the
// engine's configuration names it.
func TestAFileClosedAfterItsWriteJoinsTheSet(t *testing.T) {
	dir := writeFiles(t, map[string][]byte{"index-4207.fits": indexOfSeries(4207, arcmin22, arcmin30)})
	s, work := startWatching(t, dir)
	write(t, filepath.Join(dir, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	waitForSeries(t, s, Indexes{series4207, series4208})
	config, _ := os.ReadFile(filepath.Join(work, "engine.cfg"))
	if !strings.Contains(string(config), "index "+filepath.Join(dir, "index-4208.fits")) {
		t.Errorf("the engine's configuration is\n%s", config)
	}
}

// A downloader that writes a temporary name and renames the file into
// place adds it at the rename.
func TestAFileRenamedIntoPlaceJoinsTheSet(t *testing.T) {
	dir := t.TempDir()
	s, _ := startWatching(t, dir)
	write(t, filepath.Join(dir, "index-4208.fits.part"), indexOfSeries(4208, arcmin30, arcmin42))
	if err := os.Rename(filepath.Join(dir, "index-4208.fits.part"), filepath.Join(dir, "index-4208.fits")); err != nil {
		t.Fatal(err)
	}
	waitForSeries(t, s, Indexes{series4208})
}

// A file that leaves the directory, by a delete or a rename away,
// leaves the set.
func TestAFileThatLeavesLeavesTheSet(t *testing.T) {
	cases := []struct {
		name  string
		leave func(dir string) error
	}{
		{"a delete", func(dir string) error { return os.Remove(filepath.Join(dir, "index-4208.fits")) }},
		{"a rename away", func(dir string) error {
			return os.Rename(filepath.Join(dir, "index-4208.fits"), filepath.Join(dir, "old-4208.fits"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeFiles(t, map[string][]byte{
				"index-4207.fits": indexOfSeries(4207, arcmin22, arcmin30),
				"index-4208.fits": indexOfSeries(4208, arcmin30, arcmin42),
			})
			s, _ := startWatching(t, dir)
			if err := c.leave(dir); err != nil {
				t.Fatal(err)
			}
			waitForSeries(t, s, Indexes{series4207})
		})
	}
}

// A file in the middle of its download when the solver starts is not
// loaded, because it ends inside a header. It joins when the writer
// closes it whole.
func TestAFileThatIsStillWrittenJoinsWhenItIsClosed(t *testing.T) {
	whole := indexOfSeries(4208, arcmin30, arcmin42)
	dir := writeFiles(t, map[string][]byte{"index-4208.fits": whole[:1000]})
	s, _ := startWatching(t, dir)
	waitForSeries(t, s, Indexes{})
	write(t, filepath.Join(dir, "index-4208.fits"), whole)
	waitForSeries(t, s, Indexes{series4208})
}

// A file that a writer opens again and writes in place leaves the set
// at its first write, so the engine never reads it half written, and
// joins again when the writer closes it.
func TestAFileWrittenInPlaceLeavesUntilItIsClosed(t *testing.T) {
	dir := writeFiles(t, map[string][]byte{"index-4208.fits": indexOfSeries(4208, arcmin30, arcmin42)})
	s, _ := startWatching(t, dir)
	f, err := os.OpenFile(filepath.Join(dir, "index-4208.fits"), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	whole := indexOfSeries(4208, arcmin30, arcmin42)
	if _, err := f.Write(whole[:1000]); err != nil {
		t.Fatal(err)
	}
	waitForSeries(t, s, Indexes{})
	if _, err := f.Write(whole[1000:]); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	waitForSeries(t, s, Indexes{series4208})
}

// A file that is not an index file but matches index-*.fits is left
// out, and the rest still load.
func TestAFileThatDoesNotReadIsLeftOut(t *testing.T) {
	dir := writeFiles(t, map[string][]byte{"index-4207.fits": indexOfSeries(4207, arcmin22, arcmin30)})
	s, _ := startWatching(t, dir)
	write(t, filepath.Join(dir, "index-bad.fits"), []byte("not FITS"))
	write(t, filepath.Join(dir, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	waitForSeries(t, s, Indexes{series4207, series4208})
}

// spec.indexFiles.path can name a directory that a Job makes later.
// The solver serves no series until the directory appears, and reads
// it then.
func TestADirectoryThatAppearsLaterIsRead(t *testing.T) {
	claim := t.TempDir()
	dir := filepath.Join(claim, "2mass", "4200")
	s, _ := startWatching(t, dir)
	waitForSeries(t, s, Indexes{})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	waitForSeries(t, s, Indexes{series4208})
}

// A directory that is removed leaves no series, and one made again in
// its place is read again.
func TestADirectoryRemovedAndMadeAgainIsReadAgain(t *testing.T) {
	claim := t.TempDir()
	dir := filepath.Join(claim, "2mass")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "index-4207.fits"), indexOfSeries(4207, arcmin22, arcmin30))
	s, _ := startWatching(t, dir)
	waitForSeries(t, s, Indexes{series4207})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitForSeries(t, s, Indexes{})
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	waitForSeries(t, s, Indexes{series4208})
}

// A watch that the kernel refuses for a reason other than a missing
// directory ends the solver mode, so the kubelet starts the pod's
// container again and its restarts show the fault.
func TestAWatchTheKernelRefusesEndsTheSolver(t *testing.T) {
	claim := t.TempDir()
	dir := filepath.Join(claim, "2mass")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := startWatching(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case err := <-s.Failed():
		if !strings.Contains(err.Error(), dir) {
			t.Errorf("the failure %q does not name the directory", err)
		}
	case <-ctx.Done():
		t.Error("the solver did not fail")
	}
}

// A path that names a file, and a missing directory under one that the
// solver cannot read, fail the start.
func TestAStartTheKernelRefusesFails(t *testing.T) {
	claim := t.TempDir()
	write(t, filepath.Join(claim, "indexes"), nil)
	if err := os.Mkdir(filepath.Join(claim, "locked"), 0o300); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, dir string }{
		{"a file", filepath.Join(claim, "indexes")},
		{"under a directory it cannot read", filepath.Join(claim, "locked", "2mass")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Start(t.Context(), Options{Indexes: c.dir, Work: t.TempDir()}, io.Discard); err == nil {
				t.Error("no error")
			}
		})
	}
}
