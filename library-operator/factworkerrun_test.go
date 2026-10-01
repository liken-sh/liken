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
	"time"
)

// What a worker container does with its list: which videos it passes over,
// which it works on, and which folders it asks the operator to rescan.

// A worker of one fact over one root, with no webhook, and the buffer its log
// goes to.
func testFactWorker(t *testing.T, worker factWorker, kind, root string) (*factWorkerRun, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	return &factWorkerRun{
		worker: worker,
		kind:   kind,
		root:   root,
		writer: newVolumeWriter("movies-trickplay"),
		log:    log,
		client: &http.Client{Timeout: time.Second},
	}, log
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

// One video on the volume and the line a list holds for it, written at
// listed.
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

// The list can be hours old when the worker reaches a video, so the worker
// reads the volume and the ledger once more first.
func TestWhichVideosAWorkerPassesOver(t *testing.T) {
	listed := ledgerTime
	cases := []struct {
		name    string
		setUp   func(t *testing.T, root string, item workItem)
		want    string
		working bool
	}{
		{name: "a video as the list names it", working: true},
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
		{name: "an attempt after the list", want: "after the list was written",
			setUp: func(t *testing.T, root string, item workItem) {
				t.Helper()
				writeFactLedger(t, filepath.Join(root, trickplayFolder), factTrickplay, likenLedger{
					Attempts: []likenAttempt{{Path: trickplayFile, At: listed.Add(time.Minute), Result: attemptError}}})
			}},
		{name: "an attempt before the list", working: true,
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

// The worker works every video the list names and still holds, and asks for
// one rescan per title folder, after the last video of that folder.
func TestAWorkerWorksItsListAndAsksForOneRescanPerTitle(t *testing.T) {
	root := t.TempDir()
	listed := time.Now().UTC()
	items := []workItem{
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E01.mkv", "one", listed),
		listedVideo(t, root, "Harbour Lights/Season 01/Harbour Lights - S01E02.mkv", "two", listed),
		{Path: "Harbour Lights/Season 01/Harbour Lights - S01E03.mkv", Size: 5, Listed: listed},
		listedVideo(t, root, "Quiet Field/Season 01/Quiet Field - S01E01.mkv", "three", listed),
	}
	if err := newVolumeWriter("movies-close").writeWorkList(root, factTrickplay, items); err != nil {
		t.Fatal(err)
	}
	var worked []string
	work, log := testFactWorker(t, recordingWorker(&worked), libraryKindSeries, root)
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
	if !strings.Contains(log.String(), "worked on 3 of the 4 videos the list named, and passed over 1") {
		t.Errorf("log = %q, want the counts of the run", log)
	}
}

// A title whose every video the worker passed over has nothing new for the
// catalog, so the worker asks for no rescan of it.
func TestAWorkerAsksForNoRescanOfATitleItPassedOver(t *testing.T) {
	root := t.TempDir()
	items := []workItem{{Path: filepath.Join(trickplayFolder, trickplayFile), Size: 5, Listed: time.Now()}}
	if err := newVolumeWriter("movies-close").writeWorkList(root, factTrickplay, items); err != nil {
		t.Fatal(err)
	}
	var worked []string
	work, _ := testFactWorker(t, recordingWorker(&worked), libraryKindMovies, root)
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
	if err := newVolumeWriter("movies-close").writeWorkList(root, factTrickplay, items); err != nil {
		t.Fatal(err)
	}
	var worked []string
	work, log := testFactWorker(t, recordingWorker(&worked), libraryKindMovies, root)
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

// A Library that has turned the fact on has no list until its next library
// Job ends, which is no work and not a failure.
func TestAWorkerWithNoListHasNoWork(t *testing.T) {
	var worked []string
	work, log := testFactWorker(t, recordingWorker(&worked), libraryKindMovies, t.TempDir())

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
			if work.root != "/library/films" || work.webhook == "" || work.worker.fact != factTrickplay {
				t.Errorf("worker = %+v, want the root under the mount, the address, and the fact", work)
			}
		})
	}
}

// The list a worker reads is the list the close container wrote, line for
// line.
func TestAWorkListReadsBackAsItWasWritten(t *testing.T) {
	root := t.TempDir()
	items := []workItem{
		{Path: "A Quiet Field (1950)/A Quiet Field (1950).mkv", Size: 3, DurationMs: 5400000, Listed: ledgerTime},
		{Path: "The Long Survey (1982)/The Long Survey (1982).mkv", Size: 7, Listed: ledgerTime},
	}

	if err := newVolumeWriter("movies-close").writeWorkList(root, factTrickplay, items); err != nil {
		t.Fatal(err)
	}
	read, err := readWorkList(root, factTrickplay)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(read, items, func(a, b workItem) bool { return a == b }) {
		t.Errorf("read %+v, want %+v", read, items)
	}
	if left := namesIn(t, filepath.Dir(workListPath(root, factTrickplay))); !slices.Equal(left, []string{"trickplay.jsonl"}) {
		t.Errorf("the list directory holds %v, want the list alone", left)
	}
}

// A list that does not parse is a list to repair, and the worker ends on it
// rather than working a part of it.
func TestAWorkListThatDoesNotParseIsAnError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, workListPath(root, factTrickplay), "{\"path\":\"A/a.mkv\"}\nnot a line of a list\n")

	if _, err := readWorkList(root, factTrickplay); err == nil {
		t.Error("readWorkList = nil error, want the line it could not read")
	}
}

// The walk skips every dot name, so the list directory never becomes a row.
func TestTheWalkNeverReadsAWorkList(t *testing.T) {
	path := workListPath("/library", factTrickplay)
	relative := strings.Split(strings.TrimPrefix(path, "/library/"), string(filepath.Separator))

	if !skipName(relative[0]) {
		t.Errorf("the walk reads %s, want the list under a name it skips", path)
	}
}
