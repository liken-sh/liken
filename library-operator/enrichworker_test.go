package main

import (
	"bytes"
	"net/http"
	"path/filepath"
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
	if work.scope != "" {
		t.Errorf("scope = %q, want the whole library", work.scope)
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

	if _, err := work.gaps(t.Context(), factProbe, ledgerTime); err == nil {
		t.Error("the gap read reported no error, want one")
	}
	if err := work.markRunStarted(t.Context()); err == nil {
		t.Error("the run mark reported no error, want one")
	}
}

// Every container of the enricher Job counts under the enricher's own worker.
func TestAnEnricherReadsItsWorkerOutOfTheEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		named string
		want  string
	}{
		{name: "a container of the enricher Job", named: workerEnrich, want: workerEnrich},
		{name: "a container of the trickplay Job", named: workerTrickplay, want: workerTrickplay},
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

// seedOldTallies writes one row this worker left more than the retention ago,
// so a test can prove the Job that follows deletes it.
func seedOldTallies(t *testing.T, catalog *Catalog, library, worker string) {
	t.Helper()
	old := newTallies(catalog, library, worker, "a-run-that-ended", factProbe,
		time.Now().UTC().Add(-tallyRetention-time.Hour))
	old.add(tallyAttempts, 1, "fact", factProbe, "result", attemptFound)
	if err := old.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A Job deletes its own worker's old rows where it marks its run's start, so
// the table holds the retention and no more.
func TestMarkRunStartedSweepsTheOldTallies(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	seedOldTallies(t, catalog, work.library, workerEnrich)

	if err := work.markRunStarted(t.Context()); err != nil {
		t.Fatal(err)
	}

	if held := talliesHeld(t, agent, work.library); len(held) != 0 {
		t.Errorf("the table holds %v, want the old run's rows gone", held)
	}
}

// A sweep the catalog refuses is logged and never ends the run, because a
// count is not the work.
func TestASweepTheCatalogRefusesNeverEndsTheRun(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work, log := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	seedOldTallies(t, catalog, work.library, workerEnrich)
	agent.transactionsLeft = 2

	err := work.markRunStarted(t.Context())

	if err != nil {
		t.Fatalf("markRunStarted = %v, want no error", err)
	}
	if !strings.Contains(log.String(), "could not sweep the tallies") {
		t.Errorf("log = %q, want the refused sweep", log.String())
	}
}
