package main

// These tests read one tree with one worker and with eight, and prove the two
// reads are the same. They also end a walk the two ways it ends: a cancelled
// context, and a collector that stops reading.

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// collectWalk reads a whole tree with this many workers and sorts the rows, so
// two walks of one tree compare equal whatever order they read it in.
func collectWalk(t *testing.T, root string, rule folderRule, workers int) *walkResult {
	t.Helper()
	was := walkWorkers
	t.Cleanup(func() { walkWorkers = was })
	walkWorkers = workers

	result := &walkResult{}
	for folder := range walkTree(context.Background(), root, rule) {
		appendFolder(result, folder)
		result.titles += folder.titles
		result.unidentified += folder.unidentified
		result.unidentifiedNames = append(result.unidentifiedNames, folder.unidentifiedNames...)
		if folder.readError {
			result.readError = true
		}
	}
	sort.Slice(result.movies, func(i, j int) bool { return result.movies[i].Id < result.movies[j].Id })
	sort.Slice(result.series, func(i, j int) bool { return result.series[i].Id < result.series[j].Id })
	sort.Slice(result.episodes, func(i, j int) bool { return result.episodes[i].Id < result.episodes[j].Id })
	sort.Slice(result.files, func(i, j int) bool { return result.files[i].Path < result.files[j].Path })
	sort.Slice(result.aliases, func(i, j int) bool {
		if result.aliases[i].Alias != result.aliases[j].Alias {
			return result.aliases[i].Alias < result.aliases[j].Alias
		}
		return result.aliases[i].Item < result.aliases[j].Item
	})
	sort.Strings(result.unidentifiedNames)
	return result
}

// A movies volume with titles at the root, under one grouping folder,
// under two nested ones, and two folders with no year.
func moviesTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	titles := []string{
		"The Signal (2024)",
		filepath.Join("Genre", "The Beacon (2019)"),
		filepath.Join("Genre", "The Crossing (2021)"),
		filepath.Join("Genre", "Studio A", "The Lantern (2016)"),
		filepath.Join("Genre", "Studio B", "The Orchard (2012)"),
		filepath.Join("Genre", "Studio B", "The Quarry (2018)"),
		"Mystery Folder",
		filepath.Join("Genre", "Another Mystery"),
	}
	for i, title := range titles {
		dir := filepath.Join(root, title)
		name := filepath.Base(title)
		writeFile(t, filepath.Join(dir, name+".mkv"), "video")
		writeFile(t, filepath.Join(dir, "folder.jpg"), "art")
		if !strings.Contains(name, "Mystery") {
			writeFile(t, filepath.Join(dir, "movie.nfo"), fmt.Sprintf(
				`<movie><title>%s</title><year>1999</year><uniqueid type="tmdb">%d</uniqueid></movie>`, name, i))
		}
	}
	return root
}

// A series volume of shows with season folders and episodes.
func seriesTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for i, show := range []string{"Breaking Bad (2008)", "The Wire (2002)", "Nameless"} {
		dir := filepath.Join(root, show)
		writeFile(t, filepath.Join(dir, "tvshow.nfo"), fmt.Sprintf(
			`<tvshow><title>%s</title><uniqueid type="tvdb">%d</uniqueid></tvshow>`, show, i))
		for season := range 2 {
			folder := fmt.Sprintf("Season %02d", season+1)
			for episode := range 2 {
				writeFile(t, filepath.Join(dir, folder,
					fmt.Sprintf("%s - S%02dE%02d.mkv", show, season+1, episode+1)), "video")
			}
		}
	}
	return root
}

// Eight workers and one read the same tree into the same rows, the same
// counts, and the same unidentified total. This is the whole contract of the
// pool: it reads faster and reads the same thing.
func TestThePoolAndOneWorkerReadTheSameTree(t *testing.T) {
	cases := []struct {
		name             string
		tree             func(*testing.T) string
		rule             func(root string) folderRule
		wantTitles       int
		wantUnidentified int
	}{
		{
			name:             "movies",
			tree:             moviesTree,
			rule:             func(root string) folderRule { return movieFolderRule(root, "house/movies", nil) },
			wantTitles:       8,
			wantUnidentified: 2,
		},
		{
			name:       "series",
			tree:       seriesTree,
			rule:       func(root string) folderRule { return seriesFolderRule(root, "house/series", nil) },
			wantTitles: 3,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := testCase.tree(t)

			alone := collectWalk(t, root, testCase.rule(root), 1)
			pooled := collectWalk(t, root, testCase.rule(root), 8)

			if alone.titles != testCase.wantTitles || alone.unidentified != testCase.wantUnidentified {
				t.Fatalf("one worker read %d titles and %d unidentified, want %d and %d",
					alone.titles, alone.unidentified, testCase.wantTitles, testCase.wantUnidentified)
			}
			if !reflect.DeepEqual(alone, pooled) {
				t.Errorf("one worker read %+v, eight read %+v", alone, pooled)
			}
		})
	}
}

// The ignore list holds across the pool, so a folder a Library names stays out
// of the walk however many workers read the tree.
func TestThePoolAndOneWorkerSkipTheSameIgnoredFolders(t *testing.T) {
	root := moviesTree(t)
	writeFile(t, filepath.Join(root, "#recycle", "Old Movie (2001)", "old.mkv"), "video")
	rule := func() folderRule { return movieFolderRule(root, "house/movies", ignoreSet{"#recycle": true}) }

	alone := collectWalk(t, root, rule(), 1)
	pooled := collectWalk(t, root, rule(), 8)

	if !reflect.DeepEqual(alone, pooled) {
		t.Errorf("one worker read %+v, eight read %+v", alone, pooled)
	}
	for _, movie := range pooled.movies {
		if movie.Title == "Old Movie" {
			t.Errorf("the walk read %q from the ignored folder", movie.Title)
		}
	}
}

// Every worker reads a folder at the same time, which is the point of the
// pool. The rule blocks until all eight have arrived, so a walk that reads one
// folder at a time never finishes this test.
func TestTheWalkReadsAFolderPerWorkerAtOnce(t *testing.T) {
	root := t.TempDir()
	for i := range walkWorkers {
		writeFile(t, filepath.Join(root, string(rune('A'+i)), "movie.mkv"), "video")
	}
	inside := make(chan struct{}, walkWorkers)
	release := make(chan struct{})
	rule := folderRule{
		isTitle: func(string) bool { return true },
		scan: func(string, *walkResult) {
			inside <- struct{}{}
			<-release
		},
	}

	read := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range walkTree(context.Background(), root, rule) {
			read++
		}
	}()

	for worker := range walkWorkers {
		select {
		case <-inside:
		case <-time.After(scanTestTimeout):
			close(release)
			t.Fatalf("%d workers read a folder at once, want %d", worker, walkWorkers)
		}
	}
	close(release)
	<-done

	if read != walkWorkers {
		t.Errorf("folders = %d, want the %d the tree holds", read, walkWorkers)
	}
}

// A cancelled context stops the pool between folders, both before the walk
// starts and in the middle of one.
func TestTheWalkStopsOnACancelledContext(t *testing.T) {
	root := t.TempDir()
	titles := 40
	for i := range titles {
		writeFile(t, filepath.Join(root, "Title", string(rune('A'+i)), "movie.mkv"), "video")
	}
	rule := movieFolderRule(root, "house/movies", nil)

	cases := []struct {
		name         string
		cancelAtOnce bool
	}{
		{name: "cancelled before the walk", cancelAtOnce: true},
		{name: "cancelled in flight", cancelAtOnce: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if testCase.cancelAtOnce {
				cancel()
			}

			read := 0
			for range walkTree(ctx, root, rule) {
				read++
				cancel()
			}

			if testCase.cancelAtOnce && read != 0 {
				t.Errorf("folders = %d, want none from a walk cancelled before it started", read)
			}
			if read >= titles {
				t.Errorf("folders = %d, want a walk that stopped short of the %d the tree holds", read, titles)
			}
		})
	}
}

// A collector that stops reading ends the walk and leaves no worker running.
// The count of goroutines returns to what it was, so a walk that ends early
// leaks none.
func TestTheWalkEndsWhenTheCollectorStops(t *testing.T) {
	root := moviesTree(t)
	running := runtime.NumGoroutine()

	read := 0
	for range walkTree(context.Background(), root, movieFolderRule(root, "house/movies", nil)) {
		read++
		break
	}

	if read != 1 {
		t.Errorf("folders = %d, want the one the caller read", read)
	}
	deadline := time.Now().Add(scanTestTimeout)
	for runtime.NumGoroutine() > running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if left := runtime.NumGoroutine(); left > running {
		t.Errorf("goroutines = %d, want no more than the %d before the walk", left, running)
	}
}

// What a worker classifies one directory as: a title folder, a grouping
// folder, the depth cap, and the two read errors. A directory that no longer
// exists is a title deleted while the walk ran, and a directory that cannot be
// read marks the pass incomplete.
func TestAFolderRuleReadsOneDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Genre", "The Beacon (2019)", "movie.mkv"), "video")
	writeFile(t, filepath.Join(root, "readme.txt"), "not a folder")
	writeFile(t, filepath.Join(root, "#recycle", "old.mkv"), "video")
	rule := movieFolderRule(root, "house/movies", ignoreSet{"#recycle": true})

	cases := []struct {
		name         string
		dir          walkDirectory
		wantTitles   int
		wantError    bool
		wantChildren []string
	}{
		{
			name:         "the root hands back its folders, and not its files or its ignored names",
			dir:          walkDirectory{path: root},
			wantChildren: []string{"Genre"},
		},
		{
			name:         "a grouping folder hands back its titles",
			dir:          walkDirectory{path: filepath.Join(root, "Genre"), depth: 1},
			wantChildren: []string{"The Beacon (2019)"},
		},
		{
			name:       "a title folder is scanned into its rows",
			dir:        walkDirectory{path: filepath.Join(root, "Genre", "The Beacon (2019)"), depth: 2},
			wantTitles: 1,
		},
		{
			name:      "a root it cannot read marks the pass incomplete",
			dir:       walkDirectory{path: filepath.Join(root, "gone")},
			wantError: true,
		},
		{
			name: "a folder below the root that no longer exists is left out",
			dir:  walkDirectory{path: filepath.Join(root, "gone"), depth: 1},
		},
		{
			name:      "a folder below the root it cannot read marks the pass incomplete",
			dir:       walkDirectory{path: filepath.Join(root, "readme.txt"), depth: 1},
			wantError: true,
		},
		{
			name: "a folder past the depth cap is not descended into",
			dir:  walkDirectory{path: root, depth: movieGroupingDepth + 1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			folder, children := rule.read(testCase.dir)

			titles, readError := 0, false
			if folder != nil {
				titles, readError = folder.titles, folder.readError
			}
			if titles != testCase.wantTitles || readError != testCase.wantError {
				t.Errorf("folder = %+v, want %d titles and a read error of %v",
					folder, testCase.wantTitles, testCase.wantError)
			}
			var names []string
			for _, child := range children {
				names = append(names, filepath.Base(child.path))
				if child.depth != testCase.dir.depth+1 {
					t.Errorf("child depth = %d, want one below %d", child.depth, testCase.dir.depth)
				}
			}
			if !reflect.DeepEqual(names, testCase.wantChildren) {
				t.Errorf("children = %v, want %v", names, testCase.wantChildren)
			}
		})
	}
}
