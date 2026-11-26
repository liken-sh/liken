package main

// The tests of the comparison that finds what a walk changed: the change of
// each video, the note the scan writes before it walks, and the first walk
// and the unsynced copy, which note nothing.

import (
	"bytes"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestTheComparisonFindsEachVideoTheWalkAddedOrReplaced(t *testing.T) {
	before := videoStates{
		"Kept (1999)/kept.mkv":      {Size: 100, Modified: 10},
		"Upgraded (2001)/up.mkv":    {Size: 100, Modified: 10},
		"Rewritten (2003)/same.mkv": {Size: 100, Modified: 10},
		"Gone (2005)/gone.mkv":      {Size: 100, Modified: 10},
	}
	after := videoStates{
		"Kept (1999)/kept.mkv":      {Size: 100, Modified: 10},
		"Upgraded (2001)/up.mkv":    {Size: 900, Modified: 20},
		"Rewritten (2003)/same.mkv": {Size: 100, Modified: 30},
		"New (2007)/new.mkv":        {Size: 500, Modified: 40},
	}

	got := changedVideos(before, after)

	want := map[string]string{
		"Upgraded (2001)/up.mkv":    videoReplaced,
		"Rewritten (2003)/same.mkv": videoReplaced,
		"New (2007)/new.mkv":        videoAdded,
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for path, change := range want {
		if got[path] != change {
			t.Errorf("change of %s = %q, want %q", path, got[path], change)
		}
	}
}

// A movie with one video in the catalog, linked to its item.
func seedMovie(t *testing.T, catalog *Catalog, id, folder string, size int64) {
	t.Helper()
	if _, err := catalog.UpsertMovies(t.Context(), []movieRow{{
		Id: id, Library: "house/movies", Kind: libraryKindMovies, Path: folder,
		Title: strings.TrimSuffix(folder, " (1999)"), Released: "1999",
	}}); err != nil {
		t.Fatal(err)
	}
	seedVideo(t, catalog, folder+"/movie.mkv", size, id)
}

// One video of the library, linked to the items it holds.
func seedVideo(t *testing.T, catalog *Catalog, path string, size int64, items ...string) {
	t.Helper()
	row := fileRow{Library: "house/movies", Path: path, Present: true, Type: fileTypeVideo,
		SizeBytes: size, Modified: size, VideoCodec: "hevc", Items: wholeItems(items...)}
	if _, err := catalog.UpsertFiles(t.Context(), []fileRow{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertFileItems(t.Context(), []fileRow{row}); err != nil {
		t.Fatal(err)
	}
}

// A scan of a Library that posts webhooks, on a phases volume of its own, whose
// copy must hold the write that target names.
func notingScanner(t *testing.T, catalog *Catalog, target syncTarget) (*scanner, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &scanner{
		library: "house/movies", kind: libraryKindMovies, root: t.TempDir(), catalog: catalog,
		log: log, board: newPhaseBoard(t.TempDir()), webhooks: true, sync: target,
		syncTimeout: time.Minute,
	}, log
}

func TestTheScanNotesTheVideosBeforeItWalks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, agent := newSQLiteCatalog(t)
		seedMovie(t, catalog, "movie:tmdb:1001", "The Long Survey (1999)", 700)
		agent.holdVersion(t, sqliteAgentActor, 5)
		scan, _ := notingScanner(t, catalog, syncTarget{actor: sqliteAgentActor, version: 5})

		scan.noteVideosBefore(t.Context())

		closing := &closeRun{enricher: &enricher{library: "house/movies", catalog: catalog, board: scan.board}}
		seedVideo(t, catalog, "Another Survey (2001)/movie.mkv", 300, "movie:tmdb:1002")
		changes, err := closing.walkChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(changes.files) != 1 || changes.files["Another Survey (2001)/movie.mkv"] != videoAdded {
			t.Errorf("changes = %v, want the one video added after the note", changes)
		}
	})
}

// A Library with no confirmed run is on its first walk, and a copy that never
// syncs reads fewer videos than the catalog holds. Neither notes anything, so
// the close container posts nothing.
func TestTheScanNotesNothingOnAFirstWalkOrAnUnsyncedCopy(t *testing.T) {
	cases := []struct {
		name   string
		target syncTarget
	}{
		{"a first walk", syncTarget{}},
		{"a copy that never syncs", syncTarget{actor: sqliteAgentActor, version: 9}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				scan, _ := notingScanner(t, catalog, c.target)

				scan.noteVideosBefore(t.Context())

				closing := &closeRun{enricher: &enricher{library: "house/movies", catalog: catalog, board: scan.board}}
				if _, err := closing.walkChanges(t.Context()); err != errNoVideosBefore {
					t.Errorf("err = %v, want %v", err, errNoVideosBefore)
				}
			})
		})
	}
}

// A note the close container cannot read, or a catalog that refuses the read
// after the walk, posts nothing, and a container with no phases volume has no
// note to read.
func TestTheCloseContainerFindsNoChangeItCannotProve(t *testing.T) {
	cases := []struct {
		name    string
		note    string
		refused bool
		board   bool
	}{
		{"a container with no phases volume", "", false, false},
		{"a note that is not JSON", "not json", false, true},
		{"a catalog that refuses the read", "{}", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			closing := &closeRun{enricher: &enricher{library: "house/movies", catalog: catalog}}
			if c.board {
				closing.board = newPhaseBoard(t.TempDir())
				if err := closing.board.writeNote(videosBeforeNote, []byte(c.note)); err != nil {
					t.Fatal(err)
				}
			}
			if c.refused {
				agent.queriesLeft = 1
			}

			changes, err := closing.walkChanges(t.Context())

			if err == nil {
				t.Errorf("changes = %v, want an error", changes)
			}
		})
	}
}

// A scan whose catalog refuses the read before the walk writes no note, so the
// close container posts nothing, and it says why in its log.
func TestAScanWhoseCatalogRefusesTheReadNotesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, agent := newSQLiteCatalog(t)
		agent.holdVersion(t, sqliteAgentActor, 5)
		scan, log := notingScanner(t, catalog, syncTarget{actor: sqliteAgentActor, version: 5})
		// The first reads prove the sync, and the read of the videos follows.
		agent.queriesLeft = 1000
		scan.noteVideosBefore(t.Context())
		reads := 1000 - agent.queriesLeft
		scan.board = newPhaseBoard(t.TempDir())
		agent.queriesLeft = reads

		scan.noteVideosBefore(t.Context())

		if _, err := scan.board.readNote(videosBeforeNote); err == nil {
			t.Error("the scan wrote a note from a read the catalog refused")
		}
		if !strings.Contains(log.String(), "posts no webhook") {
			t.Errorf("log = %q, want the reason the Job posts nothing", log.String())
		}
	})
}
