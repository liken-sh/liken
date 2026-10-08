package main

// factworkerrun.go is the container of a worker Job's pod, which works one
// video. It reads the index the Job controller gave the pod, reads the video
// at that index of the list from the bus (worklist.go), checks the video
// against the volume once more, does the fact's work on it, and asks the
// operator to rescan the title's folder, so the catalog reads the new outputs
// and attempts within seconds and not at the next walk. It writes no catalog
// row, only files on the volume, so it needs no hand-off.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The role a worker Job's container runs.
const workerMode = "worker"

// The environment of a worker container beside the Library's own: the one
// fact it runs, the address of the Library's webhook on the operator, the
// library Job whose list it works, and the time that Job's enrich run
// finished, in RFC 3339, which is the time the gap counts from. The Job
// controller sets the index in each pod of an Indexed Job.
const (
	libraryFactVariable     = "LIBRARY_FACT"
	libraryWebhookVariable  = "LIBRARY_WEBHOOK"
	workListVariable        = "LIBRARY_WORK_LIST"
	gapSinceVariable        = "LIBRARY_GAP_SINCE"
	completionIndexVariable = "JOB_COMPLETION_INDEX"
)

// How long one rescan request may take. The operator answers at once and
// creates the Job later, so a request that takes longer is one the operator
// did not receive.
const workerWebhookTimeout = 10 * time.Second

// How long the read of the pod's video from the bus may take. The read is a
// connect, a subscribe, and one ping, so a broker that takes longer is one
// the pod cannot reach, and the pod fails for the Job to retry.
const workItemTimeout = time.Minute

// One worker container: the Library it works for, the volume it writes, the
// one video of the list it works, and where it sends a rescan.
type factWorkerRun struct {
	worker factWorker
	// The Library's key, its namespace and its name.
	library string
	kind    string
	root    string
	writer  *volumeWriter
	log     io.Writer
	// The Library's webhook address, and empty where the operator named none.
	webhook string
	client  *http.Client
	// The list this pod reads its video from, the pod's index in it, the
	// base of the list's topics, and how the pod reaches the broker.
	list  workList
	index int
	base  string
	dial  func(context.Context) (net.Conn, error)
	// The time the gap counts from: when the enrich run finished. An attempt
	// at or after it may not be in the catalog yet.
	since time.Time
}

// The role's whole program. A failure is a non-zero exit, so the Job retries
// the index, up to its backoff.
func runWorker() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	run, err := newFactWorkerRun(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "library.liken.sh: %v\n", err)
		stop()
		os.Exit(1)
	}
	if err := run.work(stopped); err != nil {
		run.logf("the %s worker failed: %v", run.worker.fact, err)
		stop()
		os.Exit(1)
	}
}

// A worker learns everything from its environment, as every container of a
// library Job does. A fact this image runs in no worker, and a pod with no
// index or no list, are a manifest to repair, so the container fails before
// it reads the volume.
func newFactWorkerRun(log io.Writer) (*factWorkerRun, error) {
	fact := os.Getenv(libraryFactVariable)
	worker, held := factWorkerOf(fact)
	if !held {
		return nil, fmt.Errorf("%s names %q, which this image runs in no worker", libraryFactVariable, fact)
	}
	index, err := strconv.Atoi(os.Getenv(completionIndexVariable))
	if err != nil || index < 0 {
		return nil, fmt.Errorf("%s is %q, which is not the index of an Indexed Job's pod",
			completionIndexVariable, os.Getenv(completionIndexVariable))
	}
	run := os.Getenv(workListVariable)
	if run == "" {
		return nil, fmt.Errorf("%s names no list", workListVariable)
	}
	namespace, name := os.Getenv(libraryNamespaceVariable), os.Getenv(libraryNameVariable)
	bus := busEndpointOf(os.Getenv(busAddressVariable), os.Getenv(topicBaseVariable))
	// A time this image cannot read is the zero time, so every attempt in
	// the ledger counts as later than the gap, and the worker passes over
	// each video the ledger has answered at all.
	since, _ := time.Parse(time.RFC3339Nano, os.Getenv(gapSinceVariable))
	return &factWorkerRun{
		worker:  worker,
		library: libraryKey(namespace, name),
		kind:    os.Getenv(libraryKindVariable),
		root:    path.Join(libraryMountPath, os.Getenv(libraryRootVariable)),
		// The pods of one Job share the Job's name, so each adds its index,
		// and no pod renames another's temporary in a folder they share.
		writer:  newVolumeWriter(writerName(os.Getenv(jobNameVariable), fact) + "-" + strconv.Itoa(index)),
		log:     log,
		webhook: os.Getenv(libraryWebhookVariable),
		client:  &http.Client{Timeout: workerWebhookTimeout},
		list:    workList{namespace: namespace, library: name, fact: fact, run: run},
		index:   index,
		base:    bus.base,
		dial:    bus.dial,
		since:   since,
	}, nil
}

// The pod's one video. A list the broker no longer holds, after a broker
// restart or a clear, leaves the pod nothing to do, and the video stays in
// the gap for the next list.
func (w *factWorkerRun) work(ctx context.Context) error {
	item, found, err := w.readItem(ctx)
	if err != nil {
		return err
	}
	if !found {
		w.logf("the broker holds no video at index %d of the %s list from the job %s, so there is no work",
			w.index, w.worker.fact, w.list.run)
		return nil
	}
	item.Listed = w.since
	if reason := w.passOver(item); reason != "" {
		w.logf("passed over %s, because %s", w.named(item.Path), reason)
		return nil
	}
	w.worker.work(ctx, w, item)
	w.rescan(ctx, w.titleFolder(item.Path))
	return nil
}

// The video at the pod's index, read in a session of its own.
func (w *factWorkerRun) readItem(ctx context.Context) (workItem, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, workItemTimeout)
	defer cancel()
	session, err := openBusSession(ctx, w.dial, w.writer.job)
	if err != nil {
		return workItem{}, false, err
	}
	defer session.close()
	return readWorkItem(session, w.base, w.list, w.index)
}

// Why the worker leaves one video of the gap alone, or empty where it works
// on it. The gap can be hours old by the time the worker reaches a video.
//
// A video that is gone has nothing to read. A video of another size is
// another file, the rule fileidentity.go holds, and the length in the gap
// belongs to the file before it, so the next walk and probe add it again.
// A ledger attempt at or after the time the gap counts from was made by a
// worker that read an earlier gap, and the catalog may not have read it.
// Its retry window still applies, so the worker does not repeat a decode
// that failed minutes ago.
func (w *factWorkerRun) passOver(item workItem) string {
	absolute := filepath.Join(w.root, item.Path)
	size, modified, err := statFile(absolute)
	if err != nil {
		return "it is not on the volume"
	}
	if identityOf(probedFile{Size: item.Size}, size, modified, lazyChangeTime(absolute)).sizeChanged {
		return "its size changed after the walk read it"
	}
	folder, entry := likenFolderFor(w.kind, absolute)
	ledger, err := readLikenLedger(folder, w.worker.fact)
	if err == nil && ledger.attemptedSince(entry, item.Listed.Unix()) {
		return "an attempt after the enrich run answered it"
	}
	return ""
}

// The folder a rescan names for one video, relative to the root: the title
// folder, which is the folder the scanner reads one title from. A path the
// title rule cannot place is named whole, and the scanner maps a file to its
// own title folder.
func (w *factWorkerRun) titleFolder(relative string) string {
	folder, held := titleFolderOf(w.root, w.kind, filepath.Join(w.root, relative))
	if !held {
		return relative
	}
	return relativePath(w.root, folder)
}

// One rescan request, in the payload shape the operator reads from Jellyfin:
// a top-level path, relative to the root. A request that fails is one log
// line, because the next walk reads the folder anyway.
func (w *factWorkerRun) rescan(ctx context.Context, folder string) {
	if w.webhook == "" {
		return
	}
	body, err := json.Marshal(map[string]string{"path": folder})
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.webhook, bytes.NewReader(body))
	if err != nil {
		w.logf("could not ask for a rescan of %s: %v", w.named(folder), err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		w.logf("could not ask for a rescan of %s: %v", w.named(folder), err)
		return
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(response.Body, webhookBodyLimit))
	if response.StatusCode/100 != 2 {
		w.logf("the operator refused the rescan of %s: %s %s", w.named(folder),
			response.Status, strings.TrimSpace(string(answer)))
		return
	}
	w.logf("asked the operator to rescan %s", w.named(folder))
}

// The fact's ledger entry and its attempt, in one write of one file. A worker
// writes no catalog row: the rescan it asks for reads the ledger into the
// catalog.
func (w *factWorkerRun) record(folder, entry, provider, result string) {
	now := time.Now().UTC()
	err := w.writer.updateLikenLedger(folder, w.worker.fact, func(ledger *likenLedger) {
		if provider != "" {
			item := likenItem{Path: entry, Provider: providerNames{provider}}
			if provider != artProviderExisting {
				item.Written = now
			}
			ledger.noteItem(item)
		}
		ledger.noteAttempt(likenAttempt{Path: entry, At: now, Result: result})
	})
	if err != nil {
		w.logf("could not record the %s attempt at %s: %v", w.worker.fact,
			w.named(filepath.Join(folder, entry)), err)
	}
}

// One line of this worker, with every path and address in an error made
// opaque, the rule every container's log follows.
func (w *factWorkerRun) logf(format string, args ...any) {
	if w.log == nil {
		return
	}
	fmt.Fprintf(w.log, "library.liken.sh: "+format+"\n", opaqueErrors(args, w.root)...)
}

// A path on the volume, absolute or relative to the root, as a line names it.
func (w *factWorkerRun) named(path string) string {
	return opaquePath(w.root, path)
}
