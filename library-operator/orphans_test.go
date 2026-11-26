package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A title folder on a fresh root, with each file the case names written
// under it. A name that ends in a slash is a directory.
func outputsTree(t *testing.T, title string, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		path := filepath.Join(root, title, name)
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(path, volumeDirectoryPerm); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, path, "bytes")
	}
	return root
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// The tiles of the old release of an upgraded film, beside the new release.
const (
	upgradedFilm = "The Long Survey (1961)"
	oldTiles     = "The Long Survey (1961) Bluray-1080p.trickplay"
	oldSheet     = oldTiles + "/320 - 10x10/0.jpg"
	newVideo     = "The Long Survey (1961) WEBDL-1080p.mkv"
)

func TestTheTileDoorRemovesOnlyTheTilesOfAGoneVideo(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		title   string
		files   []string
		entry   string
		removed bool
	}{
		{name: "the tiles of the replaced release", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet}, entry: oldTiles, removed: true},
		{name: "a video that is there", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet, "The Long Survey (1961) Bluray-1080p.mkv"}, entry: oldTiles},
		{name: "a video that a download manager moved aside", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet, "The Long Survey (1961) Bluray-1080p.mkv.backup~"}, entry: oldTiles},
		{name: "a video that is still being copied", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet, "The Long Survey (1961) Bluray-1080p.mkv.partial~"}, entry: oldTiles},
		{name: "a video of the same name with another extension", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet, "The Long Survey (1961) Bluray-1080p.mp4"}, entry: oldTiles},
		{name: "a video of the same name in another case", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, oldSheet, "the long survey (1961) bluray-1080p.MKV"}, entry: oldTiles},
		{name: "a title with no video left", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{"movie.nfo", oldSheet}, entry: oldTiles},
		{name: "the tiles of a gone extra", kind: libraryKindMovies, title: upgradedFilm,
			files: []string{newVideo, "Extras/Making Of.trickplay/320 - 10x10/0.jpg"},
			entry: "Extras/Making Of.trickplay", removed: true},
		{name: "the tiles of a gone episode", kind: libraryKindSeries, title: "Tin Roof",
			files: []string{"Season 01/Tin Roof S01E02.mkv", "Season 01/Tin Roof S01E01.trickplay/320 - 10x10/0.jpg"},
			entry: "Season 01/Tin Roof S01E01.trickplay", removed: true},
		{name: "an episode's tiles where the series has no video left", kind: libraryKindSeries, title: "Tin Roof",
			files: []string{"tvshow.nfo", "Season 01/Tin Roof S01E01.trickplay/320 - 10x10/0.jpg"},
			entry: "Season 01/Tin Roof S01E01.trickplay"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := outputsTree(t, test.title, test.files...)
			tiles := filepath.Join(root, test.title, test.entry)

			removed, err := newVolumeWriter("movies-walk").removeOrphanTiles(root, test.kind, test.title, test.entry)

			if err != nil {
				t.Fatal(err)
			}
			if removed != test.removed || exists(tiles) == test.removed {
				t.Errorf("removed = %v and the tiles are there = %v, want removed %v", removed, exists(tiles), test.removed)
			}
			if test.removed && slices.ContainsFunc(namesIn(t, filepath.Dir(tiles)), func(name string) bool {
				return strings.Contains(name, likenTempMark)
			}) {
				t.Errorf("a temporary is left in %v", namesIn(t, filepath.Dir(tiles)))
			}
		})
	}
}

// The door builds the path from the title and the entry, and refuses each
// entry that could name anything but one tile directory of the title.
func TestTheTileDoorRefusesAnEntryOutsideTheTitle(t *testing.T) {
	for _, entry := range []string{
		"/etc/x.trickplay",
		"../Other (1999)/x.trickplay",
		"Extras/Deeper/x.trickplay",
		"The Long Survey (1961) Bluray-1080p.mkv",
		"",
	} {
		t.Run(entry, func(t *testing.T) {
			root := outputsTree(t, upgradedFilm, newVideo, oldSheet)

			removed, err := newVolumeWriter("movies-walk").removeOrphanTiles(root, libraryKindMovies, upgradedFilm, entry)

			if removed || err == nil {
				t.Errorf("removed = %v, err = %v, want a refusal", removed, err)
			}
		})
	}
}

func TestTheTileDoorKeepsASymbolicLink(t *testing.T) {
	root := outputsTree(t, upgradedFilm, newVideo, "Elsewhere/320 - 10x10/0.jpg")
	link := filepath.Join(root, upgradedFilm, oldTiles)
	if err := os.Symlink(filepath.Join(root, upgradedFilm, "Elsewhere"), link); err != nil {
		t.Fatal(err)
	}

	removed, _ := newVolumeWriter("movies-walk").removeOrphanTiles(root, libraryKindMovies, upgradedFilm, oldTiles)

	if removed || !exists(link) || !exists(filepath.Join(root, upgradedFilm, "Elsewhere/320 - 10x10/0.jpg")) {
		t.Errorf("removed = %v, want the link and what it names kept", removed)
	}
}

// A volume that refuses the remove answers with the refusal, which the close
// container counts, and the tiles stay.
func TestTheTileDoorReportsAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	root := outputsTree(t, upgradedFilm, newVideo, oldSheet)
	folder := filepath.Join(root, upgradedFilm)
	if err := os.Chmod(folder, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, volumeDirectoryPerm) })

	removed, err := newVolumeWriter("movies-walk").removeOrphanTiles(root, libraryKindMovies, upgradedFilm, oldTiles)

	if removed || err == nil || !exists(filepath.Join(folder, oldTiles)) {
		t.Errorf("removed = %v, err = %v, want a refusal and the tiles kept", removed, err)
	}
}

// The records of the old release, in the appearances and titles directories
// of the folder that held it.
var oldRecords = []string{
	".liken/appearances/The Long Survey (1961) Bluray-1080p.mkv.jsonl",
	".liken/appearances/The Long Survey (1961) Bluray-1080p.mkv.matches.json",
	".liken/appearances/The Long Survey (1961) Bluray-1080p.mkv.spans.json",
	".liken/titles/The Long Survey (1961) Bluray-1080p.mkv.jsonl",
}

const oldVideo = "The Long Survey (1961) Bluray-1080p.mkv"

func TestTheRecordsDoorRemovesOnlyTheRecordsOfAGoneVideo(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		removed bool
	}{
		{name: "the records of the replaced release", files: []string{newVideo}, removed: true},
		{name: "a video that is there", files: []string{newVideo, oldVideo}},
		{name: "a video that a download manager moved aside", files: []string{newVideo, oldVideo + ".backup~"}},
		{name: "a video that is still being copied", files: []string{newVideo, oldVideo + ".partial~"}},
		{name: "a title with no video left", files: []string{"movie.nfo"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := outputsTree(t, upgradedFilm, append(slices.Clone(test.files), oldRecords...)...)
			gallery := filepath.Join(root, upgradedFilm, ".liken/appearances/gallery.json")
			writeFile(t, gallery, "{}")

			removed, err := newVolumeWriter("movies-walk").removeOrphanRecords(root, libraryKindMovies, upgradedFilm, oldVideo)

			if err != nil {
				t.Fatal(err)
			}
			if removed != test.removed {
				t.Errorf("removed = %v, want %v", removed, test.removed)
			}
			for _, record := range oldRecords {
				if exists(filepath.Join(root, upgradedFilm, record)) == test.removed {
					t.Errorf("%s is there = %v, want removed %v", record, !test.removed, test.removed)
				}
			}
			if !exists(gallery) {
				t.Error("the gallery of the title is gone")
			}
		})
	}
}

func TestTheRecordsDoorRefusesAnEntryOutsideTheTitle(t *testing.T) {
	for _, entry := range []string{"/etc/x.mkv", "../Other (1999)/x.mkv", "Extras/Deeper/x.mkv", "notes.txt", ""} {
		t.Run(entry, func(t *testing.T) {
			root := outputsTree(t, upgradedFilm, newVideo)

			removed, err := newVolumeWriter("movies-walk").removeOrphanRecords(root, libraryKindMovies, upgradedFilm, entry)

			if removed || err == nil {
				t.Errorf("removed = %v, err = %v, want a refusal", removed, err)
			}
		})
	}
}

func TestTheRecordsDoorKeepsASymbolicLink(t *testing.T) {
	root := outputsTree(t, upgradedFilm, newVideo, "elsewhere.jsonl")
	link := filepath.Join(root, upgradedFilm, oldRecords[0])
	if err := os.MkdirAll(filepath.Dir(link), volumeDirectoryPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, upgradedFilm, "elsewhere.jsonl"), link); err != nil {
		t.Fatal(err)
	}

	removed, _ := newVolumeWriter("movies-walk").removeOrphanRecords(root, libraryKindMovies, upgradedFilm, oldVideo)

	if removed || !exists(link) {
		t.Errorf("removed = %v, want the link kept", removed)
	}
}

// A trickplay staging that a killed worker left, in both of the forms the
// releases named it.
const (
	staleStaging    = ".The Long Survey (1961) WEBDL-1080p.trickplay" + likenTempMark + "movies-trickplay-a1-trickplay-0"
	undottedStaging = "The Long Survey (1961) WEBDL-1080p.trickplay" + likenTempMark + "movies-trickplay-a1-trickplay-0"
	asideTree       = ".The Long Survey (1961) WEBDL-1080p.trickplay.earlier" + likenTempMark + "movies-trickplay-a1-trickplay-0"
)

// The kernel sets a change time to the real time, so these tests run outside
// a synctest bubble and age a temporary by passing a later time.
func TestTheTemporaryDoorRemovesOnlyAStaleTemporary(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		path    string
		later   time.Duration
		removed bool
	}{
		{name: "a staging two days old", files: []string{newVideo, staleStaging + "/320 - 10x10/0.jpg"},
			path: staleStaging, later: staleTemporaryAge + time.Hour, removed: true},
		{name: "a staging with no leading dot", files: []string{newVideo, undottedStaging + "/320 - 10x10/0.jpg"},
			path: undottedStaging, later: staleTemporaryAge + time.Hour, removed: true},
		{name: "a temporary file two days old", files: []string{newVideo, ".movie.nfo" + likenTempMark + "movies-walk-nfo"},
			path: ".movie.nfo" + likenTempMark + "movies-walk-nfo", later: staleTemporaryAge + time.Hour, removed: true},
		{name: "a staging a worker still writes", files: []string{newVideo, staleStaging + "/320 - 10x10/0.jpg"},
			path: staleStaging, later: time.Hour},
		{name: "a name without the mark", files: []string{newVideo, ".hidden/0.jpg"},
			path: ".hidden", later: staleTemporaryAge + time.Hour},
		{name: "an aside tree whose tiles are missing", files: []string{newVideo, asideTree + "/320 - 10x10/0.jpg"},
			path: asideTree, later: staleTemporaryAge + time.Hour},
		{name: "an aside tree beside its tiles", removed: true, later: staleTemporaryAge + time.Hour, path: asideTree,
			files: []string{newVideo, asideTree + "/320 - 10x10/0.jpg",
				"The Long Survey (1961) WEBDL-1080p.trickplay/320 - 10x10/0.jpg"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := outputsTree(t, upgradedFilm, test.files...)
			path := filepath.Join(root, upgradedFilm, test.path)

			removed, err := newVolumeWriter("movies-walk").removeStaleTemporary(path, time.Now().Add(test.later))

			if removed != test.removed || exists(path) == test.removed {
				t.Errorf("removed = %v (%v) and the temporary is there = %v, want removed %v",
					removed, err, exists(path), test.removed)
			}
		})
	}
}

func TestTheTemporaryDoorRefusesAPathUnderLiken(t *testing.T) {
	root := outputsTree(t, upgradedFilm, newVideo, ".liken/identity.yaml"+likenTempMark+"movies-walk")
	path := filepath.Join(root, upgradedFilm, ".liken/identity.yaml"+likenTempMark+"movies-walk")

	removed, err := newVolumeWriter("movies-walk").removeStaleTemporary(path, time.Now().Add(staleTemporaryAge+time.Hour))

	if removed || err == nil || !exists(path) {
		t.Errorf("removed = %v, err = %v, want a refusal", removed, err)
	}
}

// The walk of a title names the outputs of its gone videos and its stale
// temporaries, and nothing of a video that is there.
func TestTheWalkOfATitleNamesTheOutputsOfItsGoneVideos(t *testing.T) {
	files := append([]string{newVideo, oldSheet, "The Long Survey (1961) WEBDL-1080p.trickplay/320 - 10x10/0.jpg",
		".liken/appearances/" + newVideo + ".jsonl", ".liken/appearances/gallery.json",
		staleStaging + "/320 - 10x10/0.jpg"}, oldRecords...)
	root := outputsTree(t, upgradedFilm, files...)
	scan := folderScan{root: root, library: "house/movies", kind: libraryKindMovies}

	swept := scan.sweepTitle(filepath.Join(root, upgradedFilm), time.Now().Add(staleTemporaryAge+time.Hour))

	want := []orphanOutput{
		{Title: upgradedFilm, Entry: oldTiles, Kind: orphanTiles},
		{Title: upgradedFilm, Entry: oldVideo, Kind: orphanRecords},
	}
	if !slices.Equal(swept.outputs, want) {
		t.Errorf("outputs = %v, want %v", swept.outputs, want)
	}
	if !slices.Equal(swept.temporaries, []string{filepath.Join(root, upgradedFilm, staleStaging)}) {
		t.Errorf("temporaries = %v, want the staging", swept.temporaries)
	}
}

func TestTheWalkWritesNoRowForTheTilesOfAGoneVideo(t *testing.T) {
	root := outputsTree(t, upgradedFilm, newVideo, oldSheet, "The Long Survey (1961) WEBDL-1080p.trickplay/320 - 10x10/0.jpg")

	result := readFolder(folderScan{root: root, library: "house/movies", kind: libraryKindMovies},
		filepath.Join(root, upgradedFilm))

	var tiles []string
	for _, row := range result.files {
		if row.Type == fileTypeTrickplay {
			tiles = append(tiles, row.Path)
		}
	}
	if !slices.Equal(tiles, []string{filepath.Join(upgradedFilm, "The Long Survey (1961) WEBDL-1080p.trickplay")}) {
		t.Errorf("tile rows = %v, want the new release's alone", tiles)
	}
}

// The scan notes the outputs only after a full walk on a synced copy, and the
// close container removes them. A webhook rescan of the same folder, and a
// walk on a copy that never reached its write, remove nothing.
func TestAFullWalkRemovesTheOutputsOfAGoneVideoAndARescanDoesNot(t *testing.T) {
	cases := []struct {
		name     string
		scanPath string
		version  int64
		removed  bool
	}{
		{name: "a full walk", version: 5, removed: true},
		{name: "a webhook rescan", scanPath: upgradedFilm, version: 5},
		{name: "a copy that never syncs", version: 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, agent := newSQLiteCatalog(t)
				run, log := closingJob(t, catalog, scanPhase)
				for _, name := range append([]string{newVideo, oldSheet}, oldRecords...) {
					writeFile(t, filepath.Join(run.root, upgradedFilm, name), "bytes")
				}
				agent.holdVersion(t, sqliteAgentActor, 5)
				scan, _, _ := scanJob(t, run.root, libraryKindMovies, c.scanPath)
				scan.catalog = catalog
				scan.board = newPhaseBoard(run.board.dir)
				scan.sync, scan.syncTimeout = syncTarget{actor: sqliteAgentActor, version: c.version}, time.Minute
				if err := scan.runPhase(t.Context()); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- run.runJob(t.Context()) }()
				confirmTheRun(t, catalog, workerEnrich, run.job)
				if err := <-done; err != nil {
					t.Fatalf("the job failed: %v", err)
				}

				if exists(filepath.Join(run.root, upgradedFilm, oldTiles)) == c.removed {
					t.Errorf("the old tiles are there = %v, want removed %v", !c.removed, c.removed)
				}
				if exists(filepath.Join(run.root, upgradedFilm, oldRecords[0])) == c.removed {
					t.Errorf("the old record is there = %v, want removed %v", !c.removed, c.removed)
				}
				if c.removed && !strings.Contains(log.String(), "removed 2 outputs of gone videos and 0 stale temporaries") {
					t.Errorf("log = %q, want the counts", log)
				}
			})
		})
	}
}

// The close container takes each output and temporary of the note through its
// door, and the one line counts each answer: removed, kept because the check
// failed again, and refused. The test runs outside a synctest bubble, because
// the temporary's age counts from the change time the kernel set.
func TestTheCloseContainerCountsWhatEachDoorAnswered(t *testing.T) {
	root := outputsTree(t, upgradedFilm, newVideo, oldVideo, oldSheet, staleStaging+"/320 - 10x10/0.jpg",
		"The Long Survey (1961) Other.trickplay/320 - 10x10/0.jpg")
	board := newPhaseBoard(t.TempDir())
	note := orphansNoteBody{
		Outputs: []orphanOutput{
			{Title: upgradedFilm, Entry: "The Long Survey (1961) Other.trickplay", Kind: orphanTiles},
			{Title: upgradedFilm, Entry: oldTiles, Kind: orphanTiles},
			{Title: upgradedFilm, Entry: "../Other (1999)/x.trickplay", Kind: orphanTiles},
			{Title: upgradedFilm, Entry: oldVideo, Kind: "sheets"},
		},
		Temporaries: []string{filepath.Join(upgradedFilm, staleStaging), "../outside" + likenTempMark + "x"},
	}
	data, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	if err := board.writeNote(orphansNote, data); err != nil {
		t.Fatal(err)
	}
	log := &bytes.Buffer{}
	run := &closeRun{enricher: &enricher{kind: libraryKindMovies, root: root, board: board,
		writer: newVolumeWriter("movies-walk-close"), log: log}}

	run.sweepOrphans(time.Now().Add(staleTemporaryAge + time.Hour))

	want := "removed 1 outputs of gone videos and 1 stale temporaries, kept 1, and could not remove 3"
	if !strings.Contains(log.String(), want) {
		t.Errorf("log = %q, want %q", log, want)
	}
	if !exists(filepath.Join(root, upgradedFilm, oldTiles)) {
		t.Error("the tiles of the video that is there are gone")
	}
}
