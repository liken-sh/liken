package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// PROSE: builds one enricher over a temporary volume and a real catalog, the
// way the container is built from its environment.
func testEnricher(t *testing.T, kind, root string, catalog *Catalog) (*enricher, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &enricher{
		library: "house/movies",
		kind:    kind,
		root:    root,
		job:     "movies-enrich",
		catalog: catalog,
		writer:  newVolumeWriter("movies-enrich"),
		log:     log,
	}, log
}

func TestAnEnricherReadsItsWholeWiringOutOfTheEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(libraryKindVariable, libraryKindMovies)
	t.Setenv(libraryRootVariable, "media")
	t.Setenv(catalogAPIVariable, "http://127.0.0.1:9999")
	t.Setenv(jobNameVariable, "movies-enrich")
	t.Setenv(scanPathVariable, "")

	work := newEnricher(&bytes.Buffer{})

	if work.library != "house/movies" || work.kind != libraryKindMovies {
		t.Errorf("enricher = %+v, want the Library the environment names", work)
	}
	if work.root != filepath.Join(libraryMountPath, "media") {
		t.Errorf("root = %q, want the root under the mount", work.root)
	}
	if work.scope != "" {
		t.Errorf("scope = %q, want the whole library", work.scope)
	}
}

func TestAnEnricherWithNoEnvironmentTakesTheDefaults(t *testing.T) {
	for _, name := range []string{libraryNamespaceVariable, libraryNameVariable, libraryKindVariable,
		libraryRootVariable, catalogAPIVariable, jobNameVariable, scanPathVariable} {
		t.Setenv(name, "")
	}

	work := newEnricher(&bytes.Buffer{})

	if work.root != libraryMountPath {
		t.Errorf("root = %q, want the mount itself", work.root)
	}
	if work.writer.job != "job" {
		t.Errorf("the writer names %q, want the fallback", work.writer.job)
	}
}

func TestANarrowedJobWorksOverItsOwnFolderAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Action", "The Thing (1982)", "thing.mkv"), "video")

	cases := []struct {
		name      string
		scanPath  string
		wantScope string
		inScope   string
		outScope  string
	}{
		{
			name:      "a relative folder under the root",
			scanPath:  "Action/The Thing (1982)",
			wantScope: filepath.Join("Action", "The Thing (1982)"),
			inScope:   filepath.Join("Action", "The Thing (1982)", "thing.mkv"),
			outScope:  filepath.Join("Action", "Alien (1979)", "alien.mkv"),
		},
		{
			name:      "the media server's own absolute path",
			scanPath:  "/data/media/Action/The Thing (1982)",
			wantScope: filepath.Join("Action", "The Thing (1982)"),
			inScope:   filepath.Join("Action", "The Thing (1982)", "thing.mkv"),
			outScope:  "Other/other.mkv",
		},
		{
			name:      "a folder the volume does not hold",
			scanPath:  "Action/Not There",
			wantScope: "",
			inScope:   "anything at all",
			outScope:  "",
		},
		{
			name:      "no folder at all",
			scanPath:  "",
			wantScope: "",
			inScope:   "anything at all",
			outScope:  "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			work, _ := testEnricher(t, libraryKindMovies, root, nil)
			work.scanPath = test.scanPath
			work.scope = work.narrowedScope()

			if work.scope != test.wantScope {
				t.Fatalf("scope = %q, want %q", work.scope, test.wantScope)
			}
			if !work.inScope(test.inScope) {
				t.Errorf("%q reads as out of scope", test.inScope)
			}
			if test.outScope != "" && work.inScope(test.outScope) {
				t.Errorf("%q reads as in scope", test.outScope)
			}
		})
	}
}

func TestTheLedgerFolderOfAFileIsTheOneTheWalkReads(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		absolute   string
		wantFolder string
		wantEntry  string
	}{
		{
			name:       "a movie's own video",
			kind:       libraryKindMovies,
			absolute:   "/library/The Thing (1982)/thing.mkv",
			wantFolder: "/library/The Thing (1982)",
			wantEntry:  "thing.mkv",
		},
		{
			name:       "a trailer beside the feature",
			kind:       libraryKindMovies,
			absolute:   "/library/The Thing (1982)/trailers/teaser.mkv",
			wantFolder: "/library/The Thing (1982)",
			wantEntry:  "trailers/teaser.mkv",
		},
		{
			name:       "an episode in a season folder",
			kind:       libraryKindSeries,
			absolute:   "/library/Twin Peaks/Season 01/s01e01.mkv",
			wantFolder: "/library/Twin Peaks/Season 01",
			wantEntry:  "s01e01.mkv",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			folder, entry := likenFolderFor(test.kind, test.absolute)
			if folder != test.wantFolder || entry != test.wantEntry {
				t.Errorf("likenFolderFor = %q, %q, want %q, %q", folder, entry, test.wantFolder, test.wantEntry)
			}
		})
	}
}

func TestAnEnricherThatCannotReachItsSidecarFails(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if _, err := work.gaps(t.Context(), concernProbe, ledgerTime); err == nil {
		t.Error("the gap read reported no error, want one")
	}
	if err := work.markRunStarted(t.Context()); err == nil {
		t.Error("the run mark reported no error, want one")
	}
}
