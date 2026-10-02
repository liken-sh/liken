package main

// factworkerrun.go is the container of a worker Job. It reads the fact's work
// list off the volume, checks each video against the list once more, does the
// fact's work on it, and asks the operator to rescan the title's folder, so
// the catalog reads the new outputs and attempts within seconds and not at
// the next walk. It holds no catalog, so it writes nothing but files on the
// volume.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The role a worker Job's container runs.
const workerMode = "worker"

// The environment of a worker container beside the Library's own: the one
// fact it runs, and the address of the Library's webhook on the operator.
const (
	libraryFactVariable    = "LIBRARY_FACT"
	libraryWebhookVariable = "LIBRARY_WEBHOOK"
)

// How long one rescan request may take. The operator answers at once and
// creates the Job later, so a request that takes longer is one the operator
// did not receive.
const workerWebhookTimeout = 10 * time.Second

// One worker container: the Library it works for, the volume it writes, and
// where it sends a rescan.
type factWorkerRun struct {
	worker factWorker
	// The Library's key, its namespace and its name, which names its list.
	library string
	kind    string
	root    string
	writer  *volumeWriter
	log     io.Writer
	// The Library's webhook address, and empty where the operator named none.
	webhook string
	client  *http.Client
	// The share of the list this pod works, which is the whole list in a Job
	// of one pod.
	share workerShare
}

// The role's whole program. A failure is a non-zero exit, so the Job fails and
// the next library Job's list starts another worker.
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
// library Job does. A fact this image runs in no worker is a manifest to
// repair, so the container fails before it reads the volume.
func newFactWorkerRun(log io.Writer) (*factWorkerRun, error) {
	fact := os.Getenv(libraryFactVariable)
	worker, held := factWorkerOf(fact)
	if !held {
		return nil, fmt.Errorf("%s names %q, which this image runs in no worker", libraryFactVariable, fact)
	}
	share, err := workerShareOf(os.Getenv(completionIndexVariable), os.Getenv(workerParallelismVariable))
	if err != nil {
		return nil, err
	}
	return &factWorkerRun{
		worker:  worker,
		library: libraryKey(os.Getenv(libraryNamespaceVariable), os.Getenv(libraryNameVariable)),
		kind:    os.Getenv(libraryKindVariable),
		root:    path.Join(libraryMountPath, os.Getenv(libraryRootVariable)),
		writer:  newVolumeWriter(share.writerName(writerName(os.Getenv(jobNameVariable), fact))),
		log:     log,
		webhook: os.Getenv(libraryWebhookVariable),
		client:  &http.Client{Timeout: workerWebhookTimeout},
		share:   share,
	}, nil
}

// The whole list, one video at a time, so one decode holds the container's
// memory. The list is sorted by path, so the videos of one title folder come
// together, and the rescan of a folder goes out once its last video is done.
// One request per folder and not per video keeps a series to one held path
// on the operator, which collapses more than heldPathLimit paths into a full
// walk.
func (w *factWorkerRun) work(ctx context.Context) error {
	items, err := readWorkList(w.root, w.library, w.worker.fact)
	if err != nil {
		return err
	}
	w.logf("read %s from the %s work list", counted(len(items), "video"), w.worker.fact)
	items = w.quickFirst(w.shareOf(items))
	worked, passed := 0, 0
	unreported := ""
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		folder := w.titleFolder(item.Path)
		if unreported != "" && folder != unreported {
			w.rescan(ctx, unreported)
			unreported = ""
		}
		if reason := w.passOver(item); reason != "" {
			w.logf("passed over %s, because %s", w.named(item.Path), reason)
			passed++
			continue
		}
		w.worker.work(ctx, w, item)
		worked++
		unreported = folder
	}
	if unreported != "" {
		w.rescan(ctx, unreported)
	}
	w.logf("worked on %d of the %s the list named, and passed over %d", worked, counted(len(items), "video"), passed)
	return nil
}

// The list with the videos the fact calls quick first. Each group keeps the
// order of the list, so the videos of one title folder stay together within
// a group. A folder whose videos fall in both groups is rescanned once after
// each group's run of it.
func (w *factWorkerRun) quickFirst(items []workItem) []workItem {
	if w.worker.quick == nil {
		return items
	}
	quick, slow := []workItem{}, []workItem{}
	for _, item := range items {
		if w.worker.quick(w, item) {
			quick = append(quick, item)
		} else {
			slow = append(slow, item)
		}
	}
	if len(quick) > 0 {
		w.logf("takes the %s that are quick to work first", counted(len(quick), "video"))
	}
	return append(quick, slow...)
}

// Why the worker leaves one video of the list alone, or empty where it works
// on it. The list can be hours old by the time the worker reaches a video.
//
// A video that is gone has nothing to read. A video of another size is
// another file, the rule fileidentity.go holds, and the length in the list
// belongs to the file before it, so the next walk and probe list it again.
// A ledger attempt at or after the list's time was made by a worker that
// read an earlier list, and the catalog had not read it when this list was
// written. Its retry window still applies, so the worker does not repeat a
// decode that failed minutes ago.
func (w *factWorkerRun) passOver(item workItem) string {
	absolute := filepath.Join(w.root, item.Path)
	size, modified, err := statFile(absolute)
	if err != nil {
		return "it is not on the volume"
	}
	if identityOf(probedFile{Size: item.Size}, size, modified, lazyChangeTime(absolute)).sizeChanged {
		return "its size changed after the list was written"
	}
	folder, entry := likenFolderFor(w.kind, absolute)
	ledger, err := readLikenLedger(folder, w.worker.fact)
	if err == nil && ledger.attemptedSince(entry, item.Listed.Unix()) {
		return "an attempt after the list was written answered it"
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
