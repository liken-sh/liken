package main

// The tests of the change of each item a walk posts (plan 100): an added
// item, a replaced item, an item that gained a version, and the videos each
// item's previous list carries with the rows the catalog held before the walk.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A catalog, the scan that notes it before a walk, and the close container
// that compares the note with the catalog after the walk.
type changeWorld struct {
	t       *testing.T
	catalog *Catalog
	agent   *sqliteAgent
	scan    *scanner
	closing *closeRun
}

func newChangeWorld(t *testing.T, kind string) *changeWorld {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	board := newPhaseBoard(t.TempDir())
	return &changeWorld{t: t, catalog: catalog, agent: agent,
		scan: &scanner{
			library: "house/movies", kind: kind, root: t.TempDir(), catalog: catalog,
			log: &strings.Builder{}, board: board, webhooks: true,
			sync: syncTarget{actor: sqliteAgentActor, version: 5}, syncTimeout: time.Minute,
		},
		closing: &closeRun{enricher: &enricher{library: "house/movies", kind: kind, catalog: catalog, board: board}},
	}
}

// One title row of the world's library.
func (w *changeWorld) title(id string) {
	w.t.Helper()
	if w.scan.kind == libraryKindSeries {
		if _, err := w.catalog.UpsertSeries(w.t.Context(), []seriesRow{{
			Id: id, Library: "house/movies", Kind: libraryKindSeries, Path: "The Long Road", Title: "The Long Road",
		}}); err != nil {
			w.t.Fatal(err)
		}
		return
	}
	if _, err := w.catalog.UpsertMovies(w.t.Context(), []movieRow{{
		Id: id, Library: "house/movies", Kind: libraryKindMovies, Path: "The Long Survey (1999)",
		Title: "The Long Survey", Released: "1999",
	}}); err != nil {
		w.t.Fatal(err)
	}
}

// The episodes of the world's series, numbered from 1 in season 1.
func (w *changeWorld) episodes(numbers ...int) {
	w.t.Helper()
	var rows []episodeRow
	for _, number := range numbers {
		rows = append(rows, episodeRow{Id: roadEpisode(number), Library: "house/movies", Kind: libraryKindSeries,
			Series: "series:tvdb:2001", Season: 1, Episode: number})
	}
	if _, err := w.catalog.UpsertEpisodes(w.t.Context(), rows); err != nil {
		w.t.Fatal(err)
	}
}

func roadEpisode(number int) string {
	return "episode:tvdb:" + string(rune('0'+number))
}

// One video with one stream, linked to its items.
func (w *changeWorld) video(path string, size int64, items ...string) {
	w.t.Helper()
	seedVideo(w.t, w.catalog, path, size, items...)
	w.exec(`INSERT OR REPLACE INTO streams (library, path, ordinal, kind, codec, height) VALUES ('house/movies', ?, 0, 'video', 'hevc', 1080)`, path)
}

// A video that left the volume, with every row the prune deletes.
func (w *changeWorld) remove(path string) {
	w.t.Helper()
	for _, table := range []string{"files", "file_items", "streams", "marks"} {
		w.exec(`DELETE FROM `+table+` WHERE library = 'house/movies' AND path = ?`, path)
	}
}

func (w *changeWorld) exec(statement string, params ...any) {
	w.t.Helper()
	if _, err := w.agent.db.Exec(statement, params...); err != nil {
		w.t.Fatal(err)
	}
}

// The scan's note before a walk of the folders named, or of the whole library
// where none are named. Each folder is on the volume with a video, so the scan
// maps it onto a title folder.
func (w *changeWorld) note(folders ...string) {
	w.t.Helper()
	for _, folder := range folders {
		if err := os.MkdirAll(filepath.Join(w.scan.root, folder), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.scan.root, folder, "movie.mkv"), nil, 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	w.agent.holdVersion(w.t, sqliteAgentActor, 5)
	w.scan.scanPaths = folders
	w.closing.scanPaths = folders
	w.scan.noteVideosBefore(w.t.Context())
	if log := w.scan.log.(*strings.Builder).String(); log != "" {
		w.t.Fatalf("the scan noted nothing: %s", log)
	}
}

// The titles of the walk's body.
func (w *changeWorld) titles() []walkTitle {
	w.t.Helper()
	changes, err := w.closing.walkChanges(w.t.Context())
	if err != nil {
		w.t.Fatal(err)
	}
	titles, err := walkTitles(w.t.Context(), w.catalog, "house/movies", w.scan.kind, changes)
	if err != nil {
		w.t.Fatal(err)
	}
	return titles
}

// One line for each item of a body: its id, its change, and the paths of its
// previous videos.
func itemLines(titles []walkTitle) []string {
	var lines []string
	line := func(id, change string, previous []walkFile) string {
		var paths []string
		for _, file := range previous {
			paths = append(paths, file.Row["path"].(string))
		}
		return id + " " + change + " [" + strings.Join(paths, " ") + "]"
	}
	for _, title := range titles {
		var previous []walkFile
		if title.Previous != nil {
			previous = *title.Previous
		}
		lines = append(lines, line(title.Row["id"].(string), title.Change, previous))
		for _, episode := range title.Episodes {
			lines = append(lines, line(episode.Row["id"].(string), episode.Change, episode.Previous))
		}
	}
	return lines
}

const survey = "movie:tmdb:1001"

func TestEachMovieHasTheChangeTheWalkMadeToIt(t *testing.T) {
	cases := []struct {
		name   string
		before func(w *changeWorld)
		walk   func(w *changeWorld)
		want   []string
	}{
		{
			name:   "a film the catalog did not hold",
			before: func(w *changeWorld) {},
			walk: func(w *changeWorld) {
				w.title(survey)
				w.video("The Long Survey (1999)/webdl.mkv", 800, survey)
			},
			want: []string{survey + " added []"},
		},
		{
			name: "a film whose only video is replaced at a new path",
			before: func(w *changeWorld) {
				w.title(survey)
				w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Survey (1999)/bluray.mkv")
				w.video("The Long Survey (1999)/webdl.mkv", 800, survey)
			},
			want: []string{survey + " replaced [The Long Survey (1999)/bluray.mkv]"},
		},
		{
			name: "a film whose video is replaced at the same path",
			before: func(w *changeWorld) {
				w.title(survey)
				w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
			},
			walk: func(w *changeWorld) {
				w.video("The Long Survey (1999)/bluray.mkv", 900, survey)
			},
			want: []string{survey + " replaced [The Long Survey (1999)/bluray.mkv]"},
		},
		{
			name: "a film that gets a second encoding",
			before: func(w *changeWorld) {
				w.title(survey)
				w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
			},
			walk: func(w *changeWorld) {
				w.video("The Long Survey (1999)/uhd.mkv", 2000, survey)
			},
			want: []string{survey + " version-added []"},
		},
		{
			name: "a film that keeps one encoding and replaces the other",
			before: func(w *changeWorld) {
				w.title(survey)
				w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
				w.video("The Long Survey (1999)/uhd.mkv", 2000, survey)
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Survey (1999)/bluray.mkv")
				w.video("The Long Survey (1999)/webdl.mkv", 800, survey)
			},
			want: []string{survey + " replaced [The Long Survey (1999)/bluray.mkv]"},
		},
		{
			name: "a film whose id changed from its folder key to a provider id",
			before: func(w *changeWorld) {
				w.title("movie:path:the-long-survey")
				w.video("The Long Survey (1999)/bluray.mkv", 700, "movie:path:the-long-survey")
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Survey (1999)/bluray.mkv")
				w.exec(`DELETE FROM movies WHERE id = 'movie:path:the-long-survey'`)
				w.title(survey)
				w.exec(`INSERT INTO aliases (library, alias, item, source) VALUES ` +
					`('house/movies', 'movie:path:the-long-survey', '` + survey + `', 'folder')`)
				w.video("The Long Survey (1999)/webdl.mkv", 800, survey)
			},
			want: []string{survey + " replaced [The Long Survey (1999)/bluray.mkv]"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newChangeWorld(t, libraryKindMovies)
				c.before(w)
				w.note("The Long Survey (1999)")
				c.walk(w)

				got := itemLines(w.titles())

				if !slices.Equal(got, c.want) {
					t.Errorf("items = %v, want %v", got, c.want)
				}
			})
		})
	}
}

func TestEachEpisodeHasTheChangeTheWalkMadeToIt(t *testing.T) {
	cases := []struct {
		name   string
		before func(w *changeWorld)
		walk   func(w *changeWorld)
		want   []string
	}{
		{
			name: "a new episode beside an upgraded episode",
			before: func(w *changeWorld) {
				w.title("series:tvdb:2001")
				w.episodes(1, 2)
				w.video("The Long Road/S01E01.mkv", 100, roadEpisode(1))
				w.video("The Long Road/S01E02 HDTV.mkv", 100, roadEpisode(2))
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Road/S01E02 HDTV.mkv")
				w.video("The Long Road/S01E02 WEBDL.mkv", 200, roadEpisode(2))
				w.episodes(3)
				w.video("The Long Road/S01E03.mkv", 300, roadEpisode(3))
			},
			want: []string{
				"series:tvdb:2001 changed []",
				roadEpisode(2) + " replaced [The Long Road/S01E02 HDTV.mkv]",
				roadEpisode(3) + " added []",
			},
		},
		{
			name:   "a series the catalog did not hold",
			before: func(w *changeWorld) {},
			walk: func(w *changeWorld) {
				w.title("series:tvdb:2001")
				w.episodes(1)
				w.video("The Long Road/S01E01.mkv", 100, roadEpisode(1))
			},
			want: []string{"series:tvdb:2001 added []", roadEpisode(1) + " added []"},
		},
		{
			name: "a new episode of a series whose other episode left",
			before: func(w *changeWorld) {
				w.title("series:tvdb:2001")
				w.episodes(1)
				w.video("The Long Road/S01E01.mkv", 100, roadEpisode(1))
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Road/S01E01.mkv")
				w.episodes(2)
				w.video("The Long Road/S01E02.mkv", 100, roadEpisode(2))
			},
			want: []string{"series:tvdb:2001 changed []", roadEpisode(2) + " added []"},
		},
		{
			name: "a file of two episodes replaced at a new path",
			before: func(w *changeWorld) {
				w.title("series:tvdb:2001")
				w.episodes(1, 2)
				w.video("The Long Road/S01E01-E02 HDTV.mkv", 100, roadEpisode(1), roadEpisode(2))
			},
			walk: func(w *changeWorld) {
				w.remove("The Long Road/S01E01-E02 HDTV.mkv")
				w.video("The Long Road/S01E01-E02 WEBDL.mkv", 200, roadEpisode(1), roadEpisode(2))
			},
			want: []string{
				"series:tvdb:2001 changed []",
				roadEpisode(1) + " replaced [The Long Road/S01E01-E02 HDTV.mkv]",
				roadEpisode(2) + " replaced [The Long Road/S01E01-E02 HDTV.mkv]",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newChangeWorld(t, libraryKindSeries)
				c.before(w)
				w.note("The Long Road")
				c.walk(w)

				got := itemLines(w.titles())

				if !slices.Equal(got, c.want) {
					t.Errorf("items = %v, want %v", got, c.want)
				}
			})
		})
	}
}

// A folders walk keeps the whole rows of each video under its folders, so a
// previous video carries its row and its streams as the catalog held them.
func TestAFoldersWalkCarriesTheWholeRowsOfAPreviousVideo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		w.title(survey)
		w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
		w.note("The Long Survey (1999)")
		w.remove("The Long Survey (1999)/bluray.mkv")
		w.video("The Long Survey (1999)/webdl.mkv", 800, survey)

		previous := (*w.titles()[0].Previous)[0]

		if previous.Row["video_codec"] != "hevc" || previous.Row["size_bytes"] != float64(700) {
			t.Errorf("row = %v, want the whole row the catalog held", previous.Row)
		}
		if len(previous.Streams) != 1 || previous.Streams[0]["codec"] != "hevc" {
			t.Errorf("streams = %v, want the one stream", previous.Streams)
		}
		if previous.Marks == nil {
			t.Error("marks = null, want the file's marks as a list")
		}
	})
}

// A full walk keeps only the path, the size, and the modified time of each
// video, so a previous video carries those three columns and no streams.
func TestAFullWalkCarriesThreeColumnsOfAPreviousVideo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newChangeWorld(t, libraryKindMovies)
		w.title(survey)
		w.video("The Long Survey (1999)/bluray.mkv", 700, survey)
		w.note()
		w.remove("The Long Survey (1999)/bluray.mkv")
		w.video("The Long Survey (1999)/webdl.mkv", 800, survey)

		previous := (*w.titles()[0].Previous)[0]

		want := map[string]any{"path": "The Long Survey (1999)/bluray.mkv", "size_bytes": int64(700), "modified": int64(700)}
		if len(previous.Row) != len(want) {
			t.Errorf("row = %v, want %v", previous.Row, want)
		}
		for column, value := range want {
			if previous.Row[column] != value {
				t.Errorf("row[%s] = %v (%T), want %v", column, previous.Row[column], previous.Row[column], value)
			}
		}
		if previous.Streams != nil || previous.Marks != nil {
			t.Errorf("streams, marks = %v, %v, want null", previous.Streams, previous.Marks)
		}
	})
}
