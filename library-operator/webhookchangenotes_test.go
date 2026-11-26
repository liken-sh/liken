package main

// The tests of the notes a body's previous videos come from: a series whose
// id changed in the walk, a note of rows past its budget, a note of rows that
// is missing or broken, a file that stops being a video, the shape of the
// document, and a folders walk that read the whole root.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
)

// An episode has no aliases, so an episode whose series changed its id in the
// walk is found by the id built from the series' older id.
func TestAnEpisodeWhoseSeriesChangedItsIdIsReplaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindSeries)
		before := episodeID("series:path:the-long-road", 1, 2)
		w.title("series:path:the-long-road")
		w.exec(`INSERT INTO episodes (library, id, series, season, episode) VALUES ('house/movies', ?, 'series:path:the-long-road', 1, 2)`, before)
		w.video("The Long Road/S01E02 HDTV.mkv", 100, before)
		w.note("The Long Road")
		w.remove("The Long Road/S01E02 HDTV.mkv")
		w.exec(`DELETE FROM series`)
		w.exec(`DELETE FROM episodes`)
		w.title("series:tvdb:2001")
		after := episodeID("series:tvdb:2001", 1, 2)
		w.exec(`INSERT INTO episodes (library, id, series, season, episode) VALUES ('house/movies', ?, 'series:tvdb:2001', 1, 2)`, after)
		w.exec(`INSERT INTO aliases (library, alias, item, source) VALUES ` +
			`('house/movies', 'series:path:the-long-road', 'series:tvdb:2001', 'folder'), ` +
			`('house/movies', 'series:tvdb:2001', 'series:tvdb:2001', 'provider')`)
		w.video("The Long Road/S01E02 WEBDL.mkv", 200, after)

		got := itemLines(w.titles())

		want := []string{"series:tvdb:2001 changed []", after + " replaced [The Long Road/S01E02 HDTV.mkv]"}
		if !slices.Equal(got, want) {
			t.Errorf("items = %v, want %v", got, want)
		}
	})
}

// A note of rows stops at its budget, and each video past the budget carries
// the three columns of the note of the videos.
func TestANoteOfRowsPastItsBudgetKeepsThreeColumns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		was := rowsBeforeBudget
		t.Cleanup(func() { rowsBeforeBudget = was })
		rowsBeforeBudget = 3000
		w := newChangeWorld(t, libraryKindMovies)
		w.title(survey)
		var paths []string
		for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
			path := "The Long Survey (1999)/" + name + ".mkv"
			paths = append(paths, path)
			w.video(path, 700, survey)
		}
		w.note("The Long Survey (1999)")
		for _, path := range paths {
			w.remove(path)
		}
		w.video("The Long Survey (1999)/webdl.mkv", 800, survey)

		note, err := w.scan.board.readNote(rowsBeforeNote)
		if err != nil {
			t.Fatal(err)
		}
		previous := *w.titles()[0].Previous

		if len(note) > rowsBeforeBudget {
			t.Errorf("note = %d bytes, want at most the budget of %d", len(note), rowsBeforeBudget)
		}
		whole, three := 0, 0
		for _, file := range previous {
			if file.Streams != nil {
				whole++
			} else if len(file.Row) == 3 {
				three++
			}
		}
		if whole == 0 || three == 0 || whole+three != len(paths) {
			t.Errorf("previous = %d whole and %d of three columns, want both and %d in all", whole, three, len(paths))
		}
	})
}

// A folders walk whose note of rows is gone carries the three columns, and one
// whose note of rows does not parse posts nothing.
func TestANoteOfRowsThatIsMissingOrBroken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		w.title(survey)
		w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
		w.note("The Long Survey (1999)")
		w.remove("The Long Survey (1999)/bluray.mkv")
		w.video("The Long Survey (1999)/webdl.mkv", 800, survey)
		if err := os.Remove(filepath.Join(w.scan.board.dir, rowsBeforeNote)); err != nil {
			t.Fatal(err)
		}

		previous := (*w.titles()[0].Previous)[0]

		if len(previous.Row) != 3 || previous.Streams != nil {
			t.Errorf("previous = %+v, want the three columns", previous)
		}
		if err := w.scan.board.writeNote(rowsBeforeNote, []byte(`{"The Long`)); err != nil {
			t.Fatal(err)
		}
		if changes, err := w.closing.walkChanges(t.Context()); err == nil {
			t.Errorf("changes = %+v, want an error, so the Job posts nothing", changes.files)
		}
	})
}

// A file that stops being a video leaves its item as a video does.
func TestAFileThatStopsBeingAVideoIsPrevious(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		w.title(survey)
		w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
		w.note("The Long Survey (1999)")
		w.exec(`UPDATE files SET type = 'other' WHERE path = 'The Long Survey (1999)/bluray.mkv'`)
		w.video("The Long Survey (1999)/webdl.mkv", 800, survey)

		got := itemLines(w.titles())

		if want := []string{survey + " replaced [The Long Survey (1999)/bluray.mkv]"}; !slices.Equal(got, want) {
			t.Errorf("items = %v, want %v", got, want)
		}
	})
}

// A movie's previous is a list even when it is empty, a series has none of its
// own, and each episode has one.
func TestTheDocumentCarriesPreviousOnEachItem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		movies := newChangeWorld(t, libraryKindMovies)
		movies.note()
		movies.title(survey)
		movies.video("The Long Survey (1999)/webdl.mkv", 800, survey)
		series := newChangeWorld(t, libraryKindSeries)
		series.note()
		series.title("series:tvdb:2001")
		series.episodes(1)
		series.video("The Long Road/S01E01.mkv", 100, roadEpisode(1))

		var movie, show map[string]any
		encoded, _ := json.Marshal(movies.titles()[0])
		_ = json.Unmarshal(encoded, &movie)
		encoded, _ = json.Marshal(series.titles()[0])
		_ = json.Unmarshal(encoded, &show)

		if previous, held := movie["previous"].([]any); !held || len(previous) != 0 {
			t.Errorf("movie previous = %v, want []", movie["previous"])
		}
		if _, held := show["previous"]; held {
			t.Errorf("series previous = %v, want none", show["previous"])
		}
		episode := show["episodes"].([]any)[0].(map[string]any)
		if previous, held := episode["previous"].([]any); !held || len(previous) != 0 {
			t.Errorf("episode previous = %v, want []", episode["previous"])
		}
	})
}

// A folders walk that named a path the scan cannot map walks the whole root.
// The scan phase notes the videos before that walk, so the note lacks the
// video the walk finds, and notes the scope after it, so the document names
// a full walk. The phase then writes its mark.
func TestAScanPhaseNotesBeforeItWalksAndTheScopeAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		found := "A Quiet Shore (2001)/movie.mkv"
		if err := os.MkdirAll(filepath.Join(w.scan.root, "A Quiet Shore (2001)"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.scan.root, found), []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.agent.holdVersion(t, sqliteAgentActor, 5)
		w.scan.scanPaths = []string{"/elsewhere/Not On The Volume (2001)"}
		w.closing.scanPaths = w.scan.scanPaths

		if err := w.scan.runPhase(t.Context()); err != nil {
			t.Fatal(err)
		}

		if end := w.scan.board.ended(scanPhase); end != (phaseEnd{ended: true}) {
			t.Errorf("mark = %+v, want a walk that finished", end)
		}
		changes, err := w.closing.walkChanges(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if changes.files[found] != videoAdded {
			t.Errorf("changes = %v, want the video the walk found, which the note made before it lacks", changes.files)
		}
		body, err := w.closing.walkBody(t.Context(), libraryRun{}, changes)
		if err != nil {
			t.Fatal(err)
		}
		if body.Walk != walkFull {
			t.Errorf("walk = %q, want %q", body.Walk, walkFull)
		}
	})
}

// The scan reads the rows of a long folder in batches, so the reads before the
// walk do not grow with each video of the folder.
func TestTheScanReadsTheRowsOfAFolderInBatches(t *testing.T) {
	reads := func(t *testing.T, videos int) int {
		var count int
		synctest.Test(t, func(t *testing.T) {
			w := newChangeWorld(t, libraryKindMovies)
			w.title(survey)
			for number := range videos {
				w.video(fmt.Sprintf("The Long Survey (1999)/part %03d.mkv", number), 700, survey)
			}
			const plenty = 10000
			w.agent.queriesLeft = plenty
			w.note("The Long Survey (1999)")
			count = plenty - w.agent.queriesLeft
		})
		return count
	}

	one, many := reads(t, 1), reads(t, 300)

	// 300 videos are three batches, and each batch after the first reads the
	// three tables once more.
	if extra := many - one; extra > 6 {
		t.Errorf("reads = %d for 1 video and %d for 300, want at most 6 more", one, many)
	}
}
