package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// What a worker container does with its gap: which videos it passes over,
// which it works on, and which folders it asks the operator to rescan.

// The Library whose gaps the worker tests read.
const testWorkLibrary = "house/movies"

// A worker of one fact over one root, with no webhook and no broker, and the
// buffer its log goes to.
func testFactWorker(t *testing.T, worker factWorker, kind, root string) (*factWorkerRun, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &factWorkerRun{
		worker:  worker,
		library: testWorkLibrary,
		kind:    kind,
		root:    root,
		writer:  newVolumeWriter("movies-trickplay-0"),
		log:     log,
		client:  &http.Client{Timeout: time.Second},
		list:    workList{namespace: "house", library: "movies", fact: worker.fact, run: "movies-walk-1"},
		base:    defaultTopicBase,
	}, log
}

// The worker at one index of a list a library Job published with the videos
// given. The gap counts from the test's start, so an attempt the worker makes
// is later than the gap.
func listWorker(t *testing.T, worker factWorker, kind, root string, items []workItem,
	index int) (*factWorkerRun, *bytes.Buffer) {
	t.Helper()
	work, log := testFactWorker(t, worker, kind, root)
	broker := newRetainBroker(t)
	session := testSession(t, broker)
	if err := publishWorkList(session, defaultTopicBase, work.list, items); err != nil {
		t.Fatal(err)
	}
	work.dial, work.index, work.since = broker.dial, index, time.Now().UTC()
	return work, log
}

// A worker that records the videos it is given and writes nothing, so a test
// of the run reads which videos reached the fact.
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

// A pod works the one video at its index, and asks for a rescan of that
// video's title folder.
func TestAWorkerWorksTheVideoAtItsIndex(t *testing.T) {
	root := t.TempDir()
	items := []workItem{
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E01.mkv", "one", time.Time{}),
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E02.mkv", "two", time.Time{}),
		listedVideo(t, root, "Quiet Field/Season 01/Quiet Field - S01E01.mkv", "three", time.Time{}),
	}
	var worked []string
	work, _ := listWorker(t, recordingWorker(&worked), libraryKindSeries, root, items, 2)
	webhooks, address := recordWebhooks(t, http.StatusNoContent)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(worked, []string{items[2].Path}) {
		t.Errorf("worked on %v, want the video at index 2", worked)
	}
	if got := webhooks.named(); !slices.Equal(got, []string{"Quiet Field"}) {
		t.Errorf("rescans named %v, want the video's title folder", got)
	}
}

// A pod whose video the broker no longer holds, after a broker restart or a
// clear, has no work and does not fail, so the Job does not retry it.
func TestAWorkerWhoseVideoIsGoneHasNoWork(t *testing.T) {
	var worked []string
	work, log := listWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir(), nil, 0)

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 0 || !strings.Contains(log.String(), "holds no video at index 0") {
		t.Errorf("worked on %v with log %q, want nothing", worked, log)
	}
}

// A pod that cannot reach the broker fails, so the Job retries its index.
func TestAWorkerThatCannotReachTheBrokerFails(t *testing.T) {
	var worked []string
	work, _ := testFactWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir())
	work.dial = func(context.Context) (net.Conn, error) { return nil, errors.New("connection refused") }

	if err := work.work(t.Context()); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("work = %v, want the dial's failure", err)
	}
}

// A video the worker passed over has nothing new for the catalog, so the
// worker asks for no rescan of it.
func TestAWorkerAsksForNoRescanOfAVideoItPassedOver(t *testing.T) {
	items := []workItem{{Path: filepath.Join(trickplayFolder, trickplayFile), Size: 5, DurationMs: 100000}}
	var worked []string
	work, _ := listWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir(), items, 0)
	webhooks, address := recordWebhooks(t, http.StatusNoContent)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := webhooks.named(); len(worked) != 0 || len(got) != 0 {
		t.Errorf("worked on %v and rescans named %v, want neither", worked, got)
	}
}

// A rescan the operator refuses is one log line with the operator's own
// words, and the work still counts.
func TestARefusedRescanIsLogged(t *testing.T) {
	root := t.TempDir()
	items := []workItem{listedVideo(t, root, "A Quiet Field (1950)/A Quiet Field (1950).mkv", "one", time.Time{})}
	var worked []string
	work, log := listWorker(t, recordingWorker(&worked), libraryKindMovies, root, items, 0)
	_, address := recordWebhooks(t, http.StatusNotFound)
	work.webhook = address

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 1 {
		t.Errorf("worked on %v, want the one video", worked)
	}
	if !strings.Contains(log.String(), "the operator refused the rescan of") || !strings.Contains(log.String(), "404") {
		t.Errorf("log = %q, want the refusal in the operator's words", log)
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
			t.Setenv(completionIndexVariable, "0")
			t.Setenv(workListVariable, "movies-walk-1")

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

// The video carries the time the enrich run finished, so an attempt the
// catalog had not read when the run ended passes the video over.
func TestTheGapCountsFromTheEnrichRun(t *testing.T) {
	root := t.TempDir()
	item := listedVideo(t, root, filepath.Join(trickplayFolder, trickplayFile), "video", time.Time{})
	writeFactLedger(t, filepath.Join(root, trickplayFolder), factTrickplay, likenLedger{
		Attempts: []likenAttempt{{Path: trickplayFile, At: ledgerTime.Add(time.Minute), Result: attemptError}}})
	var worked []string
	work, log := listWorker(t, recordingWorker(&worked), libraryKindMovies, root, []workItem{item}, 0)
	work.since = ledgerTime

	if err := work.work(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(worked) != 0 || !strings.Contains(log.String(), "an attempt after the enrich run answered it") {
		t.Errorf("worked on %v with log %q, want the video passed over", worked, log)
	}
}
