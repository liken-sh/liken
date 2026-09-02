package main

// PROSE: this file is what the probe and identity containers share: the
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

// PROSE: one enricher container: the Library it serves, the volume it writes,
// and the catalog it reads its gap out of.
type enricher struct {
	library  string
	kind     string
	root     string
	scanPath string
	job      string
	catalog  *Catalog
	writer   *volumeWriter
	log      io.Writer
	// PROSE: the folder this Job was narrowed to, relative to the library
	// root, and empty where the Job covers the whole library.
	scope string
}

// PROSE: says why a container with no API credential learns everything from
// its environment, as the scanner does.
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

	work := &enricher{
		library:  libraryKey(namespace, name),
		kind:     os.Getenv(libraryKindVariable),
		root:     mountRoot,
		scanPath: os.Getenv(scanPathVariable),
		job:      job,
		catalog:  NewCatalog(api, &http.Client{Timeout: catalogWriteTimeout}),
		writer:   newVolumeWriter(job),
		log:      log,
	}
	work.scope = work.narrowedScope()
	return work
}

// PROSE: says why a Job that names a folder the volume does not hold covers
// the whole library rather than nothing.
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

// PROSE: says how a narrowed Job tells a path it owns from one it does not.
func (e *enricher) inScope(relative string) bool {
	if e.scope == "" || e.scope == "." {
		return true
	}
	return relative == e.scope || strings.HasPrefix(relative, e.scope+string(filepath.Separator))
}

// PROSE: reads one concern's work list out of the local copy of the catalog,
// which is the same query the reporter counts the gap with.
func (e *enricher) gaps(ctx context.Context, concern string, now time.Time) ([]string, error) {
	cutoff := now.Add(-defaultRetryInterval).Unix()
	keys, err := e.catalog.queryStrings(ctx, gapQueries[concern], []any{e.library, cutoff})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", concern, e.library, err)
	}
	return keys, nil
}

// PROSE: says why the probe container writes the started mark for the whole
// Job: it is the first container, and the operator reads a run in flight off a
// start with no finish beside it.
func (e *enricher) markRunStarted(ctx context.Context) error {
	run := libraryRun{Worker: workerEnrich, Job: e.job, Started: time.Now().UTC()}
	if err := e.catalog.UpsertRun(ctx, e.library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", e.library, err)
	}
	return nil
}

// PROSE: says why an attempt is recorded whatever the outcome, and why the
// entry path is relative to the folder that holds the .liken directory.
func (e *enricher) recordAttempt(folder, concern, entryPath, result string, at time.Time) {
	err := e.writer.updateLikenLedger(folder, concern, func(ledger *likenLedger) {
		ledger.noteAttempt(likenAttempt{Path: entryPath, At: at, Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", concern, entryPath, err)
	}
}

func (e *enricher) logf(format string, args ...any) {
	if e.log == nil {
		return
	}
	fmt.Fprintf(e.log, "library.liken.sh: "+format+"\n", args...)
}

// PROSE: names the folder whose .liken directory records a file concern's
// attempt: the folder the walk reads a sidecar from, which is the title folder
// where the file sits in a movie's extras.
func likenFolderFor(kind, absolute string) (string, string) {
	dir := filepath.Dir(absolute)
	if kind == libraryKindMovies && extrasFolderName(filepath.Base(dir)) != "" {
		return filepath.Dir(dir), filepath.Join(filepath.Base(dir), filepath.Base(absolute))
	}
	return dir, filepath.Base(absolute)
}
