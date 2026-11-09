package main

// The tests of the .nfo edits that run at the same time: an nfo fact that
// writes after another container edited the same file keeps that
// container's element, and an edit waits for the lock another edit holds.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An answerer that edits the .nfo file while it is asked, the way the probe
// container writes <fileinfo> while the nfo container waits for a provider.
type editingAnswerer struct {
	fakeAnswerer
	edit func()
}

func (a *editingAnswerer) answer(ctx context.Context, fact string, title titleRef) (factAnswer, bool, error) {
	a.edit()
	return a.fakeAnswerer.answer(ctx, fact, title)
}

// The nfo fact reads the file again under the lock before it writes, so the
// probe's element survives the plot.
func TestAnNFOFactKeepsAnElementWrittenWhileItAsked(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Winter Harbour (2011)"
	nfoPath := seedNFOGap(t, catalog, root, folder, "movie:tmdb:4242")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.writer.locks = t.TempDir()
	probe := newVolumeWriter("movies-probe")
	probe.locks = work.writer.locks
	fake := &editingAnswerer{
		fakeAnswerer: fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()},
		edit: func() {
			if err := probe.editNFO(nfoPath, nfoRootMovie, "Winter Harbour", xmlElement{name: "fileinfo"},
				[]byte("<fileinfo><streamdetails></streamdetails></fileinfo>")); err != nil {
				t.Fatal(err)
			}
		},
	}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	written := readFileString(t, nfoPath)
	if !strings.Contains(written, "<fileinfo>") || !strings.Contains(written, "A keeper watches the ice.") {
		t.Errorf(".nfo file = %s, want the probe's element and the plot", written)
	}
}

// A phases volume that refuses the lock leaves the .nfo file as it was, and
// the attempt is an error, so the error window asks again. A write with no
// lock could lose another container's edit.
func TestAnNFOFactWhoseLockIsRefusedIsAnError(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	nfoPath := seedNFOGap(t, catalog, root, "Winter Harbour (2011)", "movie:tmdb:4242")
	before := readFileString(t, nfoPath)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.writer.locks = filepath.Join(t.TempDir(), "a-file")
	writeFile(t, work.writer.locks, "not a directory")
	fake := &fakeAnswerer{name: "tmdb", facts: nfoFacts, answers: harbourAnswers()}

	if err := work.nfoGap(t.Context(), factOverview, lineOf(fake)); err != nil {
		t.Fatal(err)
	}

	if readFileString(t, nfoPath) != before {
		t.Error("the .nfo file changed, want every byte as it was")
	}
	if attempts := attemptsBeside(t, nfoPath, factOverview); len(attempts) != 1 || attempts[0].Result != attemptError {
		t.Errorf("attempts = %+v, want one error", attempts)
	}
}

// An edit waits while another edit of the same file holds the lock.
func TestAnNFOEditWaitsForTheLock(t *testing.T) {
	root := t.TempDir()
	nfoPath := root + "/movie.nfo"
	writer := newVolumeWriter("movies-probe")
	writer.locks = t.TempDir()
	release, err := lockNFO(writer.locks, nfoPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- writer.editNFO(nfoPath, nfoRootMovie, "Landfall", xmlElement{name: "fileinfo"},
			[]byte("<fileinfo></fileinfo>"))
	}()

	select {
	case <-done:
		t.Fatal("the edit wrote while another edit held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
