package main

// enrichworker.go is what the probe and identity containers share: the
// environment they read, the gap query they work from, and where each of them
// records what it did.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// One enricher container: the Library it serves, the volume it writes, and
// the catalog it reads its gap out of.
type enricher struct {
	library  string
	kind     string
	root     string
	scanPath string
	job      string
	catalog  *Catalog
	writer   *volumeWriter
	log      io.Writer
	// The folder this Job was narrowed to, relative to the library root, and
	// empty where the Job covers the whole library.
	scope string
	// The status topic and the bound are what the wait for the synced copy
	// needs: the topic the standing pod reports the library on, and how long a
	// container waits for its own copy to hold what that report counts.
	statusTopic string
	syncTimeout time.Duration
	// The providers a container can ask, built once and held here, so a provider
	// that spends its day in one fact is not asked again in the next fact of the
	// same container.
	providers *answerLine
	// The providers the art container can ask, built once and held here, so
	// the settings one of them states are read once for the whole container.
	art *artLine
}

// A container with no API credential learns everything from its environment,
// as the scanner does.
func newEnricher(log io.Writer) *enricher {
	namespace := os.Getenv(libraryNamespaceVariable)
	name := os.Getenv(libraryNameVariable)
	root := os.Getenv(libraryRootVariable)
	if root == "" {
		root = "/"
	}
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}
	mountRoot := path.Join(libraryMountPath, root)
	job := os.Getenv(jobNameVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}

	work := &enricher{
		library:     libraryKey(namespace, name),
		kind:        os.Getenv(libraryKindVariable),
		root:        mountRoot,
		scanPath:    os.Getenv(scanPathVariable),
		job:         job,
		catalog:     NewCatalog(api, &http.Client{Timeout: catalogWriteTimeout}),
		writer:      newVolumeWriter(job),
		log:         log,
		statusTopic: libraryStatusTopic(base, namespace, name),
		syncTimeout: syncTimeout(os.Getenv(syncTimeoutVariable)),
	}
	work.scope = work.narrowedScope()
	return work
}

// A Job that names a folder the volume does not hold covers the whole library
// and not nothing, because a folder that moved still has gaps somewhere.
func (e *enricher) narrowedScope() string {
	if e.scanPath == "" {
		return ""
	}
	absolute := resolveVolumePath(e.root, e.scanPath)
	if absolute == "" {
		e.logf("could not map %s onto the volume, working over the whole library", e.scanPath)
		return ""
	}
	return relativePath(e.root, absolute)
}

// How a narrowed Job tells a path it owns from one it does not: the path is
// the scope or sits under it.
func (e *enricher) inScope(relative string) bool {
	if e.scope == "" || e.scope == "." {
		return true
	}
	return relative == e.scope || strings.HasPrefix(relative, e.scope+string(filepath.Separator))
}

// Reads one fact's work list out of the local copy of the catalog, with
// the same query the reporter counts the gap with.
func (e *enricher) gaps(ctx context.Context, fact string, now time.Time) ([]string, error) {
	keys, err := e.catalog.queryStrings(ctx, gapQueries[fact], gapParams(e.library, now))
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", fact, e.library, err)
	}
	return keys, nil
}

// The probe container writes the started mark for the whole Job. It is the
// first container to run, and the operator reads a run in flight off a start
// with no finish beside it. Only the last container would leave the Job
// looking idle until the end.
func (e *enricher) markRunStarted(ctx context.Context) error {
	run := libraryRun{Worker: workerEnrich, Job: e.job, Started: time.Now().UTC()}
	if err := e.catalog.UpsertRun(ctx, e.library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", e.library, err)
	}
	return nil
}

// An attempt is recorded whatever the outcome, so a miss is a fact with a
// date and never a hole a fact falls into every run. The entry path is
// relative to the folder that holds the .liken directory, which is how the
// scanner keys it.
func (e *enricher) recordAttempt(folder, fact, entryPath, result string, at time.Time) {
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		ledger.noteAttempt(likenAttempt{Path: entryPath, At: at, Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", fact, entryPath, err)
	}
}

func (e *enricher) logf(format string, args ...any) {
	if e.log == nil {
		return
	}
	fmt.Fprintf(e.log, "library.liken.sh: "+format+"\n", args...)
}

// The folder whose .liken directory records a file fact's attempt: the
// folder the walk reads a sidecar from, which is the title folder even where
// the file sits in a movie's extras.
func likenFolderFor(kind, absolute string) (string, string) {
	dir := filepath.Dir(absolute)
	if kind == libraryKindMovies && extrasFolderName(filepath.Base(dir)) != "" {
		return filepath.Dir(dir), filepath.Join(filepath.Base(dir), filepath.Base(absolute))
	}
	return dir, filepath.Base(absolute)
}
