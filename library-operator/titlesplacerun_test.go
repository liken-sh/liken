package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A stand-in for `titles place`. It appends each run's arguments to a file,
// keeps a copy of the list of videos, and prints the answers given, or fails
// with the tool's words where there are none.
func installStandInPlace(t *testing.T, answers string) string {
	t.Helper()
	dir := t.TempDir()
	printed := filepath.Join(dir, "answers.jsonl")
	writeFile(t, printed, answers)
	script := "#!/bin/sh\necho \"$*\" >> '" + filepath.Join(dir, "calls") + "'\ncp \"$3\" '" +
		filepath.Join(dir, "videos.txt") + "'\n"
	if answers == "" {
		script += "echo 'titles: a reading does not load' >&2\nexit 1\n"
	} else {
		script += "cat '" + printed + "'\n"
	}
	binary := filepath.Join(dir, "titles")
	writeFile(t, binary, script)
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	held := titlesBinary
	t.Cleanup(func() { titlesBinary = held })
	titlesBinary = binary
	return dir
}

// One line of what `place` prints, for a video under the root.
func placeAnswer(root, video string, start any, confidence float64) string {
	line, _ := json.Marshal(map[string]any{"video": filepath.Join(root, video), "start": start,
		"confidence": confidence, "from": 5100.0, "duration": 6000.0})
	return string(line) + "\n"
}

// The titles library of five films in the catalog and on a volume with a
// model. A, B, and E have a reading on the volume, C has a reading the
// catalog names and the volume does not hold, and D has none. The catalog
// holds the placements of an earlier run: A's at another start, B's, D's,
// and E's as this run places it. A has a credits chapter, so the estimates
// give it a row.
func seedPlacingLibrary(t *testing.T, catalog *Catalog) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, likenDirectory, titlesRecordsDirectory, titlesModelName), "{}")
	landed := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	seed := &walkResult{}
	for _, film := range []string{"A", "B", "C", "D", "E"} {
		video := film + "/" + strings.ToLower(film) + ".mkv"
		file := placingVideo(video, "movie:tmdb:"+film)
		if film == "A" {
			file.CreditsChapterMs = 5500000
		}
		seed.files = append(seed.files, file)
		seed.movies = append(seed.movies, movieRow{Id: "movie:tmdb:" + film, Library: titlesLibrary,
			Kind: libraryKindMovies, Path: film})
		if film != "D" {
			seed.attempts = append(seed.attempts, readingAt(video, attemptFound, landed))
		}
		if film != "C" && film != "D" {
			seedReadVideo(t, root, video, "")
		}
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	rows := []markRow{placementRow("A/a.mkv", 5000000, 0.5), placementRow("B/b.mkv", 5200000, 0.7),
		placementRow("D/d.mkv", 5300000, 0.9), placementRow("E/e.mkv", 5450000, 0.45)}
	if _, err := catalog.UpsertMarks(t.Context(), nil, rows); err != nil {
		t.Fatal(err)
	}
	return root
}

// The answers `place` gives for A, B, and E of seedPlacingLibrary.
func placingAnswers(root string) string {
	return placeAnswer(root, "A/a.mkv", 5400.0, 0.91) + placeAnswer(root, "B/b.mkv", nil, 0.2) +
		placeAnswer(root, "E/e.mkv", 5450.0, 0.45)
}

var placingRunModel = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func testTitlesPlace(t *testing.T, catalog *Catalog, root string) (*titlesPlaceRun, *retainBroker, *bytes.Buffer) {
	t.Helper()
	broker := newRetainBroker(t)
	log := &bytes.Buffer{}
	return &titlesPlaceRun{namespace: "studio", name: "movies", root: root, job: "movies-titles-place-1",
		catalog: catalog, syncTimeout: scanTestTimeout, handoffTimeout: scanTestTimeout, model: placingRunModel,
		log: log, base: defaultTopicBase, dial: broker.dial, scratch: t.TempDir()}, broker, log
}

// Runs the placing to its end, with a catalog pod that confirms its run.
func placeAndConfirm(t *testing.T, run *titlesPlaceRun) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run.place(t.Context()) }()
	confirmTheRunOf(t, run.catalog, titlesLibrary, titlesPlaceMode, run.job)
	return <-done
}

func publishedPlacing(t *testing.T, broker *retainBroker) titlesPlacing {
	t.Helper()
	payload, held := broker.retainedOn(titlesPlaceTopic(defaultTopicBase, "studio", "movies"))
	if !held {
		t.Fatal("the broker holds no placing")
	}
	var placing titlesPlacing
	if err := json.Unmarshal(payload, &placing); err != nil {
		t.Fatal(err)
	}
	return placing
}

// A placing from start to end: `place` takes every reading the volume holds
// in one run on four threads, the rows follow its answers, the estimates
// are derived again, and the result on the bus holds the counts.
func TestAPlacingWritesItsAnswersAndPublishesTheCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := seedPlacingLibrary(t, catalog)
		tool := installStandInPlace(t, placingAnswers(root))
		run, broker, log := testTitlesPlace(t, catalog, root)

		if err := placeAndConfirm(t, run); err != nil {
			t.Fatal(err)
		}

		calls, _ := os.ReadFile(filepath.Join(tool, "calls"))
		model := filepath.Join(root, likenDirectory, titlesRecordsDirectory, titlesModelName)
		if want := "--model " + model + " --threads 4"; !strings.Contains(string(calls), want) ||
			strings.Count(string(calls), "place") != 1 {
			t.Errorf("calls = %q, want one place with %q", calls, want)
		}
		videos, _ := os.ReadFile(filepath.Join(tool, "videos.txt"))
		if want := filepath.Join(root, "A/a.mkv") + "\n" + filepath.Join(root, "B/b.mkv") + "\n" +
			filepath.Join(root, "E/e.mkv") + "\n"; string(videos) != want {
			t.Errorf("videos = %q, want %q", videos, want)
		}
		held, err := catalog.titlesPlacements(t.Context(), titlesLibrary)
		if err != nil {
			t.Fatal(err)
		}
		if len(held) != 2 || *held["A/a.mkv"].Start != 5400000 || *held["A/a.mkv"].Confidence != 0.91 ||
			*held["E/e.mkv"].Start != 5450000 {
			t.Errorf("placements = %+v, want A's new placement and E's", held)
		}
		estimates, _ := catalog.queryStrings(t.Context(),
			`SELECT path || ' ' || source FROM marks WHERE library = ? AND ordinal = ?`,
			[]any{titlesLibrary, creditsEstimateOrdinal})
		if len(estimates) != 1 || estimates[0] != "A/a.mkv "+markSourceTitles {
			t.Errorf("estimates = %v, want A's from its sure placement, derived after the placing", estimates)
		}
		placing := publishedPlacing(t, broker)
		want := titlesPlaced{Result: titlesPlacePlaced, Model: placingRunModel, Readings: 4,
			NewestReading: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Placements: 2, Sure: 1, None: 1, Missing: 1}
		placing.At = time.Time{}
		if placing.Kind != titlesKindEnd || placing.Job != run.job || placing.titlesPlaced != want {
			t.Errorf("placing = %+v, want %+v", placing, want)
		}
		wantOneLine(t, log, "placed the end titles of 3 readings: 2 placements, 1 of them sure, and 1 with no "+
			"end titles; wrote 1 row and deleted 2")
		if strings.Contains(log.String(), "a.mkv") {
			t.Errorf("log = %q, want no title", log)
		}
	})
}

// A volume with no model, and a `place` that fails, leave the placements as
// they were and say why on the bus, so the operator starts no run on the same
// readings again.
func TestAPlacingThatCannotPlaceKeepsTheRows(t *testing.T) {
	cases := []struct {
		name    string
		noModel bool
		reason  string
	}{
		{name: "no model", noModel: true, reason: "the volume holds no model"},
		{name: "the tool fails", reason: "a reading does not load"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				root := seedPlacingLibrary(t, catalog)
				if test.noModel {
					if err := os.Remove(filepath.Join(root, likenDirectory, titlesRecordsDirectory, titlesModelName)); err != nil {
						t.Fatal(err)
					}
				}
				installStandInPlace(t, "")
				run, broker, _ := testTitlesPlace(t, catalog, root)

				if err := placeAndConfirm(t, run); err != nil {
					t.Fatal(err)
				}

				placing := publishedPlacing(t, broker)
				if placing.Result != titlesPlaceFailed || !strings.Contains(placing.Reason, test.reason) ||
					placing.Readings != 4 || placing.Placements != 4 || !placing.Model.Equal(placingRunModel) {
					t.Errorf("placing = %+v, want failed with %q and the counts it read", placing, test.reason)
				}
				if held, _ := catalog.titlesPlacements(t.Context(), titlesLibrary); len(held) != 4 {
					t.Errorf("placements = %d, want the 4 of the run before", len(held))
				}
				runs, _ := catalog.Runs(t.Context())
				if ran, _ := runOf(runs[titlesLibrary], titlesPlaceMode); !strings.Contains(ran.Failure, test.reason) {
					t.Errorf("run = %+v, want the reason as its failure", ran)
				}
			})
		})
	}
}

// A placing that cannot reach the broker fails, so the Job runs it again,
// and one that is stopped publishes nothing.
func TestAPlacingThatCannotPublishFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		root := seedPlacingLibrary(t, catalog)
		installStandInPlace(t, placingAnswers(root))
		run, _, _ := testTitlesPlace(t, catalog, root)
		run.dial = func(context.Context) (net.Conn, error) { return nil, errors.New("no broker") }

		if err := placeAndConfirm(t, run); err == nil || !strings.Contains(err.Error(), "no broker") {
			t.Errorf("place = %v, want the broker's error", err)
		}
	})
}

// A catalog that refuses the reads fails the run before it places anything.
func TestAPlacingTheCatalogRefusesFails(t *testing.T) {
	cases := []struct {
		name    string
		queries int
	}{
		{name: "the readings", queries: 1},
		{name: "the placements", queries: 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			root := seedPlacingLibrary(t, catalog)
			installStandInPlace(t, placingAnswers(root))
			run, broker, _ := testTitlesPlace(t, catalog, root)
			agent.queriesLeft = test.queries

			if err := run.place(t.Context()); err == nil {
				t.Error("place = nil error, want the refusal")
			}
			if _, held := broker.retainedOn(titlesPlaceTopic(defaultTopicBase, "studio", "movies")); held {
				t.Error("the broker holds a placing, want none")
			}
		})
	}
}
