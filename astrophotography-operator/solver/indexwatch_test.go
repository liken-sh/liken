package solver

// These tests use the real inotify on a directory in t.TempDir(), so
// they run outside a synctest bubble. A goroutine that waits on a file
// descriptor is not durably blocked, so a bubble would never settle.
// Each wait for the index set has a timeout, so a watch that misses an
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

// startWatching starts the solver on a directory of index files, with
// its own work directory, and stops it when the test ends.
func startWatching(t *testing.T, indexes string) (*Server, string) {
	t.Helper()
	work := t.TempDir()
	return startUntilCleanup(t, Options{Indexes: indexes, Work: work, CPULimit: 60}, io.Discard), work
}

// waitForSeries waits until the server serves the series, and fails
// the test when it does not within 5 seconds.
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
		t.Fatalf("writing %s: %v", path, err)
	}
}

// When a Job downloads an index file into the claim while the solver
// runs, the file joins the set when the Job closes it, and the engine's
// configuration lists it.
func TestAFileClosedAfterItsWriteJoinsTheSet(t *testing.T) {
	dir := writeFiles(t, map[string][]byte{"index-4207.fits": indexOfSeries(4207, arcmin22, arcmin30)})
	s, work := startWatching(t, dir)
	write(t, filepath.Join(dir, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	waitForSeries(t, s, Indexes{series4207, series4208})
	config, _ := os.ReadFile(filepath.Join(work, "engine.cfg"))
	if !strings.Contains(string(config), "index "+filepath.Join(dir, "index-4208.fits")) {
		t.Errorf("the engine's configuration is\n%s\nwant a line for index-4208.fits", config)
	}
}

// A downloader that writes a temporary name and renames the file into
// place adds it at the rename.
func TestAFileRenamedIntoPlaceJoinsTheSet(t *testing.T) {
	dir := t.TempDir()
	s, _ := startWatching(t, dir)
	write(t, filepath.Join(dir, "index-4208.fits.part"), indexOfSeries(4208, arcmin30, arcmin42))
	if err := os.Rename(filepath.Join(dir, "index-4208.fits.part"), filepath.Join(dir, "index-4208.fits")); err != nil {
		t.Fatalf("renaming the index file into place: %v", err)
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
				t.Fatalf("making the index file leave the directory by %s: %v", c.name, err)
			}
			waitForSeries(t, s, Indexes{series4207})
		})
	}
}

// A file that is partway through its download when the solver starts is
// not loaded, because it ends inside a header. It joins when the writer
// closes it complete.
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
		t.Fatalf("opening the index file for writing: %v", err)
	}
	whole := indexOfSeries(4208, arcmin30, arcmin42)
	if _, err := f.Write(whole[:1000]); err != nil {
		t.Fatalf("writing the first part of the index file: %v", err)
	}
	waitForSeries(t, s, Indexes{})
	if _, err := f.Write(whole[1000:]); err != nil {
		t.Fatalf("writing the rest of the index file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing the index file: %v", err)
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
		t.Fatalf("creating the index directory: %v", err)
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
		t.Fatalf("creating the index directory: %v", err)
	}
	write(t, filepath.Join(dir, "index-4207.fits"), indexOfSeries(4207, arcmin22, arcmin30))
	s, _ := startWatching(t, dir)
	waitForSeries(t, s, Indexes{series4207})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the index directory: %v", err)
	}
	waitForSeries(t, s, Indexes{})
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the index directory: %v", err)
	}
	// The file arrives by a rename, which is one event. A file written
	// in place could be loaded by the read of the new directory and then
	// produce IN_MODIFY and IN_CLOSE_WRITE, and the watch would write the
	// configuration again while the cleanup removes the work directory.
	write(t, filepath.Join(claim, "index-4208.fits"), indexOfSeries(4208, arcmin30, arcmin42))
	if err := os.Rename(filepath.Join(claim, "index-4208.fits"), filepath.Join(dir, "index-4208.fits")); err != nil {
		t.Fatalf("renaming the index file into place: %v", err)
	}
	waitForSeries(t, s, Indexes{series4208})
}

// A watch that the kernel refuses for a reason other than a missing
// directory ends the solver, so the kubelet restarts the pod's
// container and the restart count shows the fault.
func TestAWatchTheKernelRefusesEndsTheSolver(t *testing.T) {
	claim := t.TempDir()
	dir := filepath.Join(claim, "2mass")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the index directory: %v", err)
	}
	s, _ := startWatching(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatalf("removing the index directory: %v", err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatalf("replacing the index directory with a file: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case err := <-s.Failed():
		if !strings.Contains(err.Error(), dir) {
			t.Errorf("the failure is %q, want it to name the directory %s", err, dir)
		}
	case <-ctx.Done():
		t.Error("the solver kept running for 5 seconds, want it to fail")
	}
}

// Start fails when the index path is a file, and when the index
// directory is missing under a directory that the solver cannot read.
func TestAStartTheKernelRefusesFails(t *testing.T) {
	claim := t.TempDir()
	write(t, filepath.Join(claim, "indexes"), nil)
	if err := os.Mkdir(filepath.Join(claim, "locked"), 0o300); err != nil {
		t.Fatalf("creating the unreadable directory: %v", err)
	}
	cases := []struct{ name, dir string }{
		{"a file", filepath.Join(claim, "indexes")},
		{"under a directory it cannot read", filepath.Join(claim, "locked", "2mass")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Start(t.Context(), Options{Indexes: c.dir, Work: t.TempDir()}, io.Discard); err == nil {
				t.Error("Start returned no error, want one")
			}
		})
	}
}
