package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// What a worker container does with its gap: which videos it passes over,
// which it works on, and which folders it asks the operator to rescan.

// The Library whose gaps the worker tests read.
const testWorkLibrary = "house/movies"

// A worker of one fact over one root, with no webhook, and the buffer its log
// goes to.
func testFactWorker(t *testing.T, worker factWorker, kind, root string) (*factWorkerRun, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &factWorkerRun{
		worker:  worker,
		library: testWorkLibrary,
		kind:    kind,
		root:    root,
		writer:  newVolumeWriter("movies-trickplay"),
		log:     log,
		client:  &http.Client{Timeout: time.Second},
	}, log
}

// A worker whose copy of the catalog holds the gap given: a feature with a
// length and no sheets for each item, which is the shape of a trickplay gap.
// The gap counts from the test's start, so an attempt the worker makes is
// later than the gap.
func gapWorker(t *testing.T, worker factWorker, kind, root string, items []workItem) (*factWorkerRun, *bytes.Buffer) {
	t.Helper()
	work, log, _ := gapWorkerAndAgent(t, worker, kind, root, items)
	return work, log
}

// The same worker, with the agent its copy is served by, so a test moves
// the copy's versions.
func gapWorkerAndAgent(t *testing.T, worker factWorker, kind, root string,
	items []workItem) (*factWorkerRun, *bytes.Buffer, *sqliteAgent) {
	t.Helper()
	work, log := testFactWorker(t, worker, kind, root)
	catalog, agent := newSQLiteCatalog(t)
	seed := &walkResult{}
	for _, item := range items {
		seed.files = append(seed.files, fileRow{Path: item.Path, Library: testWorkLibrary, Present: true,
			Type: fileTypeVideo, Role: fileRolePrimary, VideoCodec: "h264", DurationMs: item.DurationMs,
			SizeBytes: item.Size})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work.catalog = catalog
	work.since = time.Now().UTC()
	return work, log, agent
}

// A worker that records the videos it is given and writes nothing, so a test
// of the loop reads which videos reached the fact.
func recordingWorker(worked *[]string) factWorker {
	return factWorker{
		fact: factTrickplay,
		work: func(_ context.Context, run *factWorkerRun, item workItem) {
			*worked = append(*worked, item.Path)
			folder, entry := likenFolderFor(run.kind, filepath.Join(run.root, item.Path))
			run.record(folder, entry, "", attemptFound)
		},
	}
}

// One video on the volume and the item a gap holds for it, with the time the
// gap counts from.
func listedVideo(t *testing.T, root, path, content string, listed time.Time) workItem {
	t.Helper()
	writeFile(t, filepath.Join(root, path), content)
	return workItem{Path: path, Size: int64(len(content)), DurationMs: 100000, Listed: listed}
}

// A stand-in for the operator's webhook that records the path each request
// names.
type webhookRecorder struct {
	mutex sync.Mutex
	paths []string
}

func (r *webhookRecorder) named() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.paths)
}

func recordWebhooks(t *testing.T, status int) (*webhookRecorder, string) {
	t.Helper()
	recorder := &webhookRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		recorder.mutex.Lock()
		recorder.paths = append(recorder.paths, extractWebhookPath(body))
		recorder.mutex.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return recorder, server.URL + webhookPathPrefix + "house/movies"
}

// The gap can be hours old when the worker reaches a video, so the worker
// reads the volume and the ledger once more first.
func TestWhichVideosAWorkerPassesOver(t *testing.T) {
	listed := ledgerTime
	cases := []struct {
		name    string
		setUp   func(t *testing.T, root string, item workItem)
		want    string
		working bool
	}{
		{name: "a video as the gap names it", working: true},
		{name: "a video that is gone", want: "not on the volume",
			setUp: func(t *testing.T, root string, item workItem) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, item.Path)); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "a video of another size", want: "size changed",
			setUp: func(t *testing.T, root string, item workItem) {
				t.Helper()
				writeFile(t, filepath.Join(root, item.Path), "a longer encode of the same title")
			}},
		{name: "an attempt after the enrich run", want: "after the enrich run",
			setUp: func(t *testing.T, root string, item workItem) {
				t.Helper()
				writeFactLedger(t, filepath.Join(root, trickplayFolder), factTrickplay, likenLedger{
					Attempts: []likenAttempt{{Path: trickplayFile, At: listed.Add(time.Minute), Result: attemptError}}})
			}},
		{name: "an attempt before the enrich run", working: true,
			setUp: func(t *testing.T, root string, item workItem) {
				t.Helper()
				writeFactLedger(t, filepath.Join(root, trickplayFolder), factTrickplay, likenLedger{
					Attempts: []likenAttempt{{Path: trickplayFile, At: listed.Add(-time.Hour), Result: attemptError}}})
			}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			item := listedVideo(t, root, filepath.Join(trickplayFolder, trickplayFile), "video", listed)
			if test.setUp != nil {
				test.setUp(t, root, item)
			}
			work, _ := testFactWorker(t, trickplayWorker, libraryKindMovies, root)

			reason := work.passOver(item)

			if (reason == "") != test.working || !strings.Contains(reason, test.want) {
				t.Errorf("passOver = %q, want %q", reason, test.want)
			}
		})
	}
}

// The worker works every video of the gap the volume still holds, and asks for
// one rescan per title folder, after the last video of that folder.
func TestAWorkerWorksItsListAndAsksForOneRescanPerTitle(t *testing.T) {
	root := t.TempDir()
	listed := time.Now().UTC()
	items := []workItem{
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E01.mkv", "one", listed),
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E02.mkv", "two", listed),
		{Path: "Harbour Lights/Season 01/Harbour Lights - S01E03.mkv", Size: 5, DurationMs: 100000, Listed: listed},
		listedVideo(t, root, "Quiet Field/Season 01/Quiet Field - S01E01.mkv", "three", listed),
	}
	var worked []string
	work, log := gapWorker(t, recordingWorker(&worked), libraryKindSeries, root, items)
	webhooks, address := recordWebhooks(t, http.StatusNoContent)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := []string{items[0].Path, items[1].Path, items[3].Path}
	if !slices.Equal(worked, want) {
		t.Errorf("worked on %v, want %v", worked, want)
	}
	if got := webhooks.named(); !slices.Equal(got, []string{"Harbour Lights", "Quiet Field"}) {
		t.Errorf("rescans named %v, want one per title folder", got)
	}
	if !strings.Contains(log.String(), "worked on 3 of the 4 videos in its share, and passed over 1") {
		t.Errorf("log = %q, want the counts of the run", log)
	}
}

// A worker whose fact names the videos that are quick to work takes those
// first, each group in the order of the gap, so a refresh that reopens
// many quick videos answers them before the slow ones.
func TestAWorkerTakesTheQuickVideosFirst(t *testing.T) {
	root := t.TempDir()
	listed := time.Now().UTC()
	items := []workItem{
		listedVideo(t, root, "Slow One/Slow One.mkv", "one", listed),
		listedVideo(t, root, "Quick One/Quick One.mkv", "two", listed),
		listedVideo(t, root, "Slow Two/Slow Two.mkv", "three", listed),
		listedVideo(t, root, "Quick Two/Quick Two.mkv", "four", listed),
	}
	var worked []string
	worker := recordingWorker(&worked)
	worker.quick = func(_ *factWorkerRun, item workItem) bool {
		return strings.HasPrefix(item.Path, "Quick")
	}
	work, _ := gapWorker(t, worker, libraryKindMovies, root, items)

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := []string{items[1].Path, items[3].Path, items[0].Path, items[2].Path}
	if !slices.Equal(worked, want) {
		t.Errorf("worked on %v, want %v", worked, want)
	}
}

// A title whose every video the worker passed over has nothing new for the
// catalog, so the worker asks for no rescan of it.
func TestAWorkerAsksForNoRescanOfATitleItPassedOver(t *testing.T) {
	root := t.TempDir()
	items := []workItem{{Path: filepath.Join(trickplayFolder, trickplayFile), Size: 5, DurationMs: 100000}}
	var worked []string
	work, _ := gapWorker(t, recordingWorker(&worked), libraryKindMovies, root, items)
	webhooks, address := recordWebhooks(t, http.StatusNoContent)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := webhooks.named(); len(got) != 0 {
		t.Errorf("rescans named %v, want none", got)
	}
}

// A rescan the operator refuses is one log line with the operator's own
// words, and the worker goes on to the next title.
func TestARefusedRescanIsLoggedAndTheWorkGoesOn(t *testing.T) {
	root := t.TempDir()
	listed := time.Now().UTC()
	items := []workItem{
		listedVideo(t, root, "A Quiet Field (1950)/A Quiet Field (1950).mkv", "one", listed),
		listedVideo(t, root, "The Long Survey (1982)/The Long Survey (1982).mkv", "two", listed),
	}
	var worked []string
	work, log := gapWorker(t, recordingWorker(&worked), libraryKindMovies, root, items)
	_, address := recordWebhooks(t, http.StatusNotFound)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 2 {
		t.Errorf("worked on %v, want both titles", worked)
	}
	if !strings.Contains(log.String(), "the operator refused the rescan of") || !strings.Contains(log.String(), "404") {
		t.Errorf("log = %q, want the refusal in the operator's words", log)
	}
}

// A gap of nothing is no work and not a failure.
func TestAWorkerWithAnEmptyGapHasNoWork(t *testing.T) {
	var worked []string
	work, log := gapWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir(), nil)

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 0 || !strings.Contains(log.String(), "read 0 videos") {
		t.Errorf("worked on %v with log %q, want nothing", worked, log)
	}
}

// A worker reads its fact, its Library, and the operator's address out of its
// environment, and refuses a fact this image runs in no worker.
func TestAWorkerReadsItsWiringOutOfTheEnvironment(t *testing.T) {
	cases := []struct {
		name string
		fact string
		ok   bool
	}{
		{name: "a heavy fact", fact: factTrickplay, ok: true},
		{name: "a fact of the library Job", fact: factPoster},
		{name: "no fact"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(libraryFactVariable, test.fact)
			t.Setenv(libraryNamespaceVariable, "house")
			t.Setenv(libraryNameVariable, "movies")
			t.Setenv(libraryKindVariable, libraryKindMovies)
			t.Setenv(libraryRootVariable, "films")
			t.Setenv(libraryWebhookVariable, "http://library-operator.media.svc/webhook/house/movies")
			t.Setenv(jobNameVariable, "movies-trickplay-1")

			work, err := newFactWorkerRun(io.Discard)

			if (err == nil) != test.ok {
				t.Fatalf("newFactWorkerRun = %v, want ok: %v", err, test.ok)
			}
			if !test.ok {
				return
			}
			if work.root != "/library/films" || work.webhook == "" || work.worker.fact != factTrickplay ||
				work.library != "house/movies" {
				t.Errorf("worker = %+v, want the root under the mount, the address, the fact, and the Library", work)
			}
		})
	}
}

// A pod reads its gap only once its copy holds the enrich run it was started
// after, so every pod of the Job reads a copy that holds the same writes.
func TestAWorkerWaitsForItsCopyBeforeItReadsTheGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		items := []workItem{listedVideo(t, root, "A Quiet Field (1950)/A Quiet Field (1950).mkv", "one", time.Time{})}
		var worked []string
		work, _, agent := gapWorkerAndAgent(t, recordingWorker(&worked), libraryKindMovies, root, items)
		work.sync, work.syncTimeout = syncTarget{actor: otherAgent, version: 40}, time.Hour
		agent.holdVersion(t, otherAgent, 12)
		done := make(chan error, 1)
		go func() { done <- work.work(t.Context()) }()

		time.Sleep(time.Minute)
		synctest.Wait()
		if len(worked) != 0 {
			t.Fatalf("worked on %v before the copy held the run", worked)
		}

		agent.holdVersion(t, otherAgent, 40)

		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(worked) != 1 {
			t.Errorf("worked on %v, want the one video of the gap", worked)
		}
	})
}

// A pod whose copy never holds the run fails at the bound, so the Job retries
// and no pod works from a gap short of the run's writes.
func TestAWorkerWhoseCopyNeverSyncsFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var worked []string
		work, _ := gapWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir(), nil)
		work.sync, work.syncTimeout = syncTarget{actor: otherAgent, version: 40}, time.Minute

		if err := work.work(t.Context()); err == nil || !strings.Contains(err.Error(), "did not reach version 40") {
			t.Errorf("work = %v, want the wait's bound", err)
		}
	})
}

// Every video of the gap carries the time the enrich run finished, so an
// attempt the catalog had not read when the run ended passes the video over.
func TestTheGapCountsFromTheEnrichRun(t *testing.T) {
	root := t.TempDir()
	item := listedVideo(t, root, filepath.Join(trickplayFolder, trickplayFile), "video", time.Time{})
	writeFactLedger(t, filepath.Join(root, trickplayFolder), factTrickplay, likenLedger{
		Attempts: []likenAttempt{{Path: trickplayFile, At: ledgerTime.Add(time.Minute), Result: attemptError}}})
	var worked []string
	work, log := gapWorker(t, recordingWorker(&worked), libraryKindMovies, root, []workItem{item})
	work.since = ledgerTime

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 0 || !strings.Contains(log.String(), "an attempt after the enrich run answered it") {
		t.Errorf("worked on %v with log %q, want the video passed over", worked, log)
	}
}
