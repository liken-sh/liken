package main

// The tests of the .nfo edits that run at the same time: an nfo fact that
// writes after another container edited the same file keeps that
// container's element, and an edit waits for the lock another edit holds.

import (
	"context"
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
		done <- writer.editNFO(nfoPath, nfoRootMovie, "Arrival", xmlElement{name: "fileinfo"},
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
