package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testEnricher builds one enricher over a temporary volume and a real
// catalog, the way the container is built from its environment.
func testEnricher(t *testing.T, kind, root string, catalog *Catalog) (*enricher, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &enricher{
		library: "house/movies",
		kind:    kind,
		root:    root,
		job:     "movies-enrich",
		worker:  workerEnrich,
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
	t.Setenv(syncTimeoutVariable, "90s")
	t.Setenv(libraryWorkerVariable, workerEnrich)

	work, err := newEnricher(&bytes.Buffer{})

	if err != nil {
		t.Fatal(err)
	}
	if work.library != "house/movies" || work.kind != libraryKindMovies {
		t.Errorf("enricher = %+v, want the Library the environment names", work)
	}
	if work.root != filepath.Join(libraryMountPath, "media") {
		t.Errorf("root = %q, want the root under the mount", work.root)
	}
	if len(work.scopes) != 0 {
		t.Errorf("scopes = %q, want the whole library", work.scopes)
	}
	if work.syncTimeout != 90*time.Second {
		t.Errorf("syncTimeout = %s, want the wait the environment names", work.syncTimeout)
	}
}

func TestAnEnricherWithNoEnvironmentTakesTheDefaults(t *testing.T) {
	for _, name := range []string{libraryNamespaceVariable, libraryNameVariable, libraryKindVariable,
		libraryRootVariable, catalogAPIVariable, jobNameVariable, scanPathVariable,
		syncTimeoutVariable} {
		t.Setenv(name, "")
	}
	t.Setenv(libraryWorkerVariable, workerEnrich)

	work, err := newEnricher(&bytes.Buffer{})

	if err != nil {
		t.Fatal(err)
	}
	if work.root != libraryMountPath {
		t.Errorf("root = %q, want the mount itself", work.root)
	}
	if work.writer.job != "job" {
		t.Errorf("the writer names %q, want the fallback", work.writer.job)
	}
	if work.syncTimeout != defaultSyncTimeout {
		t.Errorf("syncTimeout = %s, want the default", work.syncTimeout)
	}
}

func TestANarrowedJobWorksOverItsOwnFoldersAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Action", "The Thing (1982)", "thing.mkv"), "video")
	writeFile(t, filepath.Join(root, "Action", "Alien (1979)", "alien.mkv"), "video")
	thing := filepath.Join("Action", "The Thing (1982)")
	alien := filepath.Join("Action", "Alien (1979)")

	cases := []struct {
		name       string
		scanPaths  []string
		wantScopes []string
		inScope    string
		outScope   string
	}{
		{
			name:       "a relative folder under the root",
			scanPaths:  []string{"Action/The Thing (1982)"},
			wantScopes: []string{thing},
			inScope:    filepath.Join(thing, "thing.mkv"),
			outScope:   filepath.Join(alien, "alien.mkv"),
		},
		{
			name:       "the media server's own absolute path",
			scanPaths:  []string{"/data/media/Action/The Thing (1982)"},
			wantScopes: []string{thing},
			inScope:    filepath.Join(thing, "thing.mkv"),
			outScope:   "Other/other.mkv",
		},
		{
			name:       "two folders",
			scanPaths:  []string{"Action/The Thing (1982)", "Action/Alien (1979)"},
			wantScopes: []string{thing, alien},
			inScope:    filepath.Join(alien, "alien.mkv"),
			outScope:   "Other/other.mkv",
		},
		{
			name:      "a folder the volume does not hold",
			scanPaths: []string{"Action/The Thing (1982)", "Action/Not There"},
			inScope:   "anything at all",
		},
		{
			name:      "the root itself",
			scanPaths: []string{"/"},
			inScope:   "anything at all",
		},
		{
			name:    "no folder at all",
			inScope: "anything at all",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			work, _ := testEnricher(t, libraryKindMovies, root, nil)
			work.scanPaths = test.scanPaths
			work.scopes = work.narrowedScopes()

			if !slices.Equal(work.scopes, test.wantScopes) {
				t.Fatalf("scopes = %q, want %q", work.scopes, test.wantScopes)
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

	if _, err := work.gaps(t.Context(), factProbe, ledgerTime); err == nil {
		t.Error("the gap read reported no error, want one")
	}
}

// A container counts under the worker its environment names.
func TestAnEnricherReadsItsWorkerOutOfTheEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		named string
		want  string
	}{
		{name: "a phase of a library Job", named: workerEnrich, want: workerEnrich},
		{name: "a container of another worker", named: workerScan, want: workerScan},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(libraryWorkerVariable, one.named)
			t.Setenv(libraryContainerVariable, factProbe)

			work, err := newEnricher(&bytes.Buffer{})

			if err != nil {
				t.Fatal(err)
			}
			if work.worker != one.want {
				t.Errorf("worker = %q, want %q", work.worker, one.want)
			}
			if work.tallies.worker != one.want {
				t.Errorf("the recorder's worker = %q, want %q", work.tallies.worker, one.want)
			}
			if work.tallies.container != factProbe {
				t.Errorf("the recorder's container = %q, want %q",
					work.tallies.container, factProbe)
			}
		})
	}
}

// A container whose environment names no worker is a manifest to repair, and
// never a run under the enricher's name.
func TestAnEnricherWithNoWorkerFails(t *testing.T) {
	t.Setenv(libraryWorkerVariable, "")

	_, err := newEnricher(&bytes.Buffer{})

	if err == nil || !strings.Contains(err.Error(), libraryWorkerVariable) {
		t.Errorf("newEnricher = %v, want an error naming %s", err, libraryWorkerVariable)
	}
}
