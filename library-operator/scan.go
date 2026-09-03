package main

// The scanner is the container of one scan Job. It walks one
// library's volume once, beside a Corrosion agent of its own on the
// Library's claim, and it holds no Kubernetes credentials. It writes the
// catalog only through that agent's transaction API, on the pod's
// loopback. It publishes no report: it writes a runs row first and last,
// and waits for the namespace's reporter to publish that row back before
// it exits, because an agent drops unsent broadcasts on SIGTERM.
//
// A Job walks the whole root, or the one folder SCAN_PATH names,
// which is the path a webhook reported. It does not use inotify, which
// fires only for writes made through the same kernel and never for
// another client's writes to a network volume.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// scanMode is the argument that selects this role. The operator writes
// it into the scanner container's command, over the image's
// entrypoint, so one image serves the operator and every scanner.
const scanMode = "scan"

// The environment the operator writes into the scanner container. The
// scanner learns which Library it serves from these alone, because the
// pod carries no API credential to look one up with.
const (
	libraryNamespaceVariable = "LIBRARY_NAMESPACE"
	libraryNameVariable      = "LIBRARY_NAME"
	libraryKindVariable      = "LIBRARY_KIND"
	libraryRootVariable      = "LIBRARY_ROOT"
	busAddressVariable       = "LIBRARY_BUS_ADDRESS"
	topicBaseVariable        = "LIBRARY_TOPIC_BASE"
	catalogAPIVariable       = "LIBRARY_CATALOG_API"
	libraryIgnoreVariable    = "LIBRARY_IGNORE"
)

// The one folder a scan Job rescans, in the form the webhook
// handler maps onto the volume. An empty value is a full walk.
const scanPathVariable = "SCAN_PATH"

// ignoreSet is the folder names the walk skips, and the test for one. A
// folder whose name is in the set, and everything under it, is left out
// of the walk. A nil set skips nothing.
type ignoreSet map[string]bool

func (s ignoreSet) skips(name string) bool {
	return s[name]
}

// parseIgnore reads the ignore list the operator JSON-encodes into the
// environment. A single JSON value carries a folder name of any
// character, and an empty or unreadable value is an empty set.
func parseIgnore(raw string) ignoreSet {
	set := ignoreSet{}
	if raw == "" {
		return set
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return set
	}
	for _, name := range names {
		set[name] = true
	}
	return set
}

// The catalog API address the scanner posts to when the environment
// names none. The agent binds it on the pod's loopback, so the scanner
// and its agent share one address.
const defaultCatalogAPI = "http://127.0.0.1:8080"

// libraryMountPath is where the operator mounts the Library's claim in
// the scanner container. The root from the Library's spec is a path
// inside that mount, so the volume's own layout is what the spec names
// and the mount point is this operator's choice.
const libraryMountPath = "/library"

// catalogWriteTimeout bounds a walk's writes to the catalog agent, so a
// stuck agent cannot hold a walk open forever.
var catalogWriteTimeout = 2 * time.Minute

// One scan Job's scanner: the root it walks, the catalog it
// writes, the run it records, and the echo it waits for. The walk's own
// counts are held under a mutex, because the walk and the run row that
// reads them are written in different steps.
type scanner struct {
	statusTopic string
	root        string
	library     string
	kind        string
	ignore      ignoreSet
	catalog     *Catalog
	bus         *Bus
	echo        *echoWaiter
	// The Job this container runs, the folder it rescans, and how
	// long it waits for the reporter to publish its run back.
	job         string
	scanPath    string
	echoTimeout time.Duration
	// log is where the scanner writes a walk that could not finish a catalog
	// step, so a swallowed error shows in the pod log instead of a gap in
	// the report. A scanner built without one writes nowhere.
	log io.Writer

	// What the walk read, which the run row carries and nothing
	// publishes.
	mutex  sync.Mutex
	report libraryReport
	// What this Job's own agent held for the library when the
	// walk ended, and whether the walk could read it.
	counts     libraryCounts
	countsRead bool

	// One walk runs at a time, so the reconciliation reads a
	// settled catalog whichever caller drives the walk.
	walkMutex sync.Mutex
}

// The scan role's whole program: read the environment, run the
// one walk, and end the process with what the Job left. A failure is a
// non-zero exit, so the Job fails and Kubernetes retries it.
func runScan() {
	// The kernel runs no default action for a signal sent to PID 1, and
	// the scanner is its container's PID 1. The signal context is what
	// ends the wait below, on the kubelet's SIGTERM or on the interrupt
	// a person who runs the binary by hand sends.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	scan, err := newScanner(time.Now().UTC(), os.Stdout)
	if err != nil {
		stop()
		os.Exit(1)
	}
	if err := scan.runJob(stopped); err != nil {
		scan.logf("the scan job failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// NewScanner reads the container's environment and builds the
// client that speaks for this Library. started is the time the walk's own
// record carries before it reads anything.
//
// It refuses to build a scanner when the environment names no broker,
// before anything is written.
func newScanner(started time.Time, log io.Writer) (*scanner, error) {
	address, err := echoBusAddress(log)
	if err != nil {
		return nil, err
	}
	namespace := os.Getenv(libraryNamespaceVariable)
	name := os.Getenv(libraryNameVariable)
	kind := os.Getenv(libraryKindVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	root := os.Getenv(libraryRootVariable)
	if root == "" {
		root = "/"
	}
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}
	ignore := parseIgnore(os.Getenv(libraryIgnoreVariable))
	mountRoot := path.Join(libraryMountPath, root)

	// One line in the pod's log says what this container was given, so
	// a person who reads the pod sees the same wiring the Library
	// declares.
	fmt.Fprintf(log, "library.liken.sh: %s/%s is a %s library at %s\n",
		namespace, name, kind, mountRoot)

	scan := &scanner{
		statusTopic: libraryStatusTopic(base, namespace, name),
		root:        mountRoot,
		library:     libraryKey(namespace, name),
		kind:        kind,
		ignore:      ignore,
		catalog:     NewCatalog(api, &http.Client{Timeout: catalogWriteTimeout}),
		log:         log,
		report:      libraryReport{LastWalk: started, LastChange: started},
		job:         os.Getenv(jobNameVariable),
		scanPath:    os.Getenv(scanPathVariable),
		echoTimeout: echoTimeout(os.Getenv(echoTimeoutVariable)),
	}
	// The Job holds no will and publishes nothing. Its one use of
	// the bus is the subscription that carries the reporter's echo back.
	scan.echo = newEchoWaiter(scan.statusTopic, scan.worker(), scan.job)
	scan.bus = newBus(address, "scan-"+namespace+"-"+name, nil, nil, scan.echo.note)
	return scan, nil
}

// The whole of a scan Job: write the run with no finish, walk,
// write the run again with what the walk left, and wait for the
// namespace's reporter to publish that run back with the counts this
// Job's own agent holds.
//
// The first write says a walk is running. The echo of the last
// write, carrying the counts, is what proves the standing pod holds
// every row this Job wrote.
func (s *scanner) runJob(ctx context.Context) error {
	run := libraryRun{Worker: s.worker(), Job: s.job, Started: time.Now().UTC()}
	if err := s.catalog.UpsertRun(ctx, s.library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", s.library, err)
	}

	walked := s.walkOnce(ctx)

	run.Finished = time.Now().UTC()
	s.mutex.Lock()
	run.Unidentified = s.report.Unidentified
	run.Removed = s.report.RemovedLastSweep
	counts, read := s.counts, s.countsRead
	s.mutex.Unlock()
	if err := s.catalog.UpsertRun(ctx, s.library, run); err != nil {
		return fmt.Errorf("writing the finished run of %s: %w", s.library, err)
	}

	if read {
		s.echo.expect(counts.items, counts.files)
	}
	if err := s.echo.wait(ctx, s.bus, s.echoTimeout); err != nil {
		return err
	}
	return walked
}

// The worker whose runs row this Job writes and whose echo it
// waits for. A Job that names a folder is the rescan worker, so its
// row stands beside the full walk's row and never over it, and the
// reporter reads the walk's own numbers off the scan row alone.
//
// A folder scan that falls back to the whole root keeps the
// rescan worker, because the Job it runs is the one the webhook asked
// for.
func (s *scanner) worker() string {
	if s.scanPath == "" {
		return workerScan
	}
	return workerRescan
}

// The one walk this Job runs: the whole root, or the single folder
// SCAN_PATH names, which falls back to the whole root when the path names
// no folder on the volume.
func (s *scanner) walkOnce(ctx context.Context) error {
	if s.scanPath == "" {
		return s.fullWalk(ctx)
	}
	absolute := s.resolveWebhookPath(s.scanPath)
	if absolute == "" {
		s.logf("could not map %s onto the volume, walking the whole root", s.scanPath)
		return s.fullWalk(ctx)
	}
	return s.rescan(ctx, absolute)
}

// walkFolders streams this library's title folders, read by the pool in
// walk.go and handed one at a time to the caller, which is the walk's one
// collector. An unknown kind streams nothing and reports zero titles rather
// than failing. A cancelled context stops the stream between folders, so a
// walk of a large volume does not run on past a shutdown.
func (s *scanner) walkFolders(ctx context.Context) iter.Seq[*walkResult] {
	switch s.kind {
	case libraryKindMovies:
		return walkTree(ctx, s.root, movieFolderRule(s.root, s.library, s.ignore))
	case libraryKindSeries:
		return walkTree(ctx, s.root, seriesFolderRule(s.root, s.library, s.ignore))
	}
	return func(yield func(*walkResult) bool) {}
}

// FullWalk is the walk's one collector. A pool of workers reads the
// root, and this goroutine takes their folders one at a time. It buffers the
// rows until the item rows reach scanFlushBatch; the file, link, and alias
// rows travel with their items and stay uncounted. It flushes each buffer:
// it upserts the rows and marks them with the walk's epoch. It then prunes
// the rows the walk did not mark, and records what it read. A write that
// fails leaves the catalog as it was and fails the Job, so the next run
// retries. An incomplete walk keeps its prune for the next clean walk, so a
// partial read never mass-deletes.
func (s *scanner) fullWalk(ctx context.Context) error {
	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	started := time.Now()
	s.logf("walking %s", s.root)

	if err := s.catalog.ensureSeen(ctx); err != nil {
		return s.walkFailed("ensure the seen table", err)
	}
	epoch := time.Now().UnixNano()

	before, err := s.catalog.countItems(ctx, s.library)
	if err != nil {
		return s.walkFailed("count the catalog before the walk", err)
	}

	buffer := &walkResult{}
	// The fold lives for the whole walk, so a set derives from every member
	// the walk reads and not from the batch its members landed in.
	sets := setFold{}
	buffered, items, titles, unidentified := 0, 0, 0, 0
	readError := false
	var unidentifiedNames []string
	flush := func() error {
		if err := ctx.Err(); err != nil {
			return s.walkFailed("write a walk batch", err)
		}
		if buffered == 0 {
			return nil
		}
		if err := flushWalk(ctx, s.catalog, buffer, epoch); err != nil {
			return s.walkFailed("write a walk batch", err)
		}
		buffer = &walkResult{}
		buffered = 0
		return nil
	}

	for folder := range s.walkFolders(ctx) {
		if folder.readError {
			readError = true
		}
		// The collector takes each folder as the worker hands it
		// over, so a failed read reaches the log at the moment it happens
		// and names the path. The summary line below reports only that the
		// pass was incomplete.
		for _, failure := range folder.readFailures {
			s.logf("could not read %s: %v", failure.path, failure.err)
		}
		appendFolder(buffer, folder)
		sets.add(folder.movies)
		found := len(folder.movies) + len(folder.series) + len(folder.episodes)
		items += found
		buffered += found
		titles += folder.titles
		unidentified += folder.unidentified
		unidentifiedNames = appendSample(unidentifiedNames, folder.unidentifiedNames, unidentifiedSample)
		if buffered >= scanFlushBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}

	// The people are read after the last title folder, because the walk of the
	// titles skips every dot directory and this store is the one exception. A
	// store the scanner cannot read marks the pass incomplete, as a title folder
	// does, so a store that would not open never sweeps its people.
	people := walkContributors(s.root, s.library)
	if people.readError {
		readError = true
	}

	// A cancelled walk read only part of the volume and wrote only part
	// of its rows, so it prunes nothing, writes no counts, and leaves the
	// last-walk time where it was.
	if err := ctx.Err(); err != nil {
		return s.walkFailed("finish the walk", err)
	}

	// An incomplete walk read only part of the volume, so its counts
	// do not describe what the volume holds. It fails the Job here without
	// pruning, so a partial read never mass-deletes.
	if incompleteWalk(readError, items, before) {
		s.logIncompleteWalk(readError, items, before)
		return errIncompleteWalk
	}

	// The sets are written after the last folder, because a set is derived
	// from all of its members. They carry the walk's own epoch, so a set
	// whose last member left the volume is unmarked and the prune below takes
	// it. A write that fails returns before the prune, because a prune with
	// no set marked would sweep every set the catalog holds.
	if err := flushWalk(ctx, s.catalog, &walkResult{sets: sets.rows()}, epoch); err != nil {
		return s.walkFailed("write the sets", err)
	}

	// The people are written with the walk's own epoch, before the prune, so a
	// person whose directory left the store is unmarked and the prune takes them.
	if err := flushWalk(ctx, s.catalog, people, epoch); err != nil {
		return s.walkFailed("write the contributors", err)
	}

	// The walk read the volume and wrote what it holds, so the count is
	// settled here. The prune and the second count read the catalog back
	// through the query API, which can miss the walk's own writes for a
	// window after a fresh agent starts. Those steps run after this point,
	// and a failure in one is logged and left for the next walk, so a read
	// that lags does not discard the count the walk already knows.
	removed := -1
	if count, err := pruneLibrary(ctx, s.catalog, s.library, epoch); err != nil {
		s.logWalk("prune the catalog", err)
	} else {
		removed = count
	}

	after, err := s.catalog.countItems(ctx, s.library)
	countedItems := err == nil
	if err != nil {
		s.logWalk("count the catalog after the walk", err)
		after = before
	}

	// The catalog's own file count, read here beside the item count.
	// A read that fails leaves the walk's own file count at zero, the way
	// the item count holds on a failed read.
	files, err := s.catalog.countFiles(ctx, s.library)
	countedFiles := err == nil
	if err != nil {
		s.logWalk("count the catalog's files", err)
	}

	now := time.Now().UTC()
	s.mutex.Lock()
	s.report.Titles = titles
	s.report.Unidentified = unidentified
	s.report.LastWalk = now
	if removed > 0 || after != before {
		s.report.LastChange = now
	}
	if removed >= 0 {
		s.report.RemovedLastSweep = removed
	}
	if countedItems {
		s.report.Items = after
	}
	if countedFiles {
		s.report.Files = files
	}
	if countedItems && countedFiles {
		s.counts = libraryCounts{items: after, files: files}
		s.countsRead = true
	}
	s.mutex.Unlock()

	s.logWalkComplete(titles, unidentified, removed, unidentifiedNames, time.Since(started))
	return nil
}

// The walk read only part of the volume, so it pruned nothing and
// kept the counts it had. The Job fails on it, because a run that read
// half a library is not a run the catalog can be reconciled against.
var errIncompleteWalk = errors.New("the walk did not read the whole volume")

// unidentifiedSample bounds how many unidentified folder names a walk
// names in its log, so a library of millions logs a sample and never one
// line per folder. The count is always reported; the names past this are a
// tally.
const unidentifiedSample = 10

// appendSample folds a folder's unidentified names into a running
// sample, stopping at the limit, so the walk holds a sample and never every
// name.
func appendSample(have, more []string, limit int) []string {
	for _, name := range more {
		if len(have) >= limit {
			return have
		}
		have = append(have, name)
	}
	return have
}

// logWalk writes one line about a walk that could not finish a step, naming
// the step and the error. It turns a swallowed catalog error into a visible
// line in the pod log, in place of a count held at its last value until the
// next walk. A scanner built without a log writes nowhere.
func (s *scanner) logWalk(step string, err error) {
	s.logf("full walk could not %s: %v", step, err)
}

// Names the step that stopped a walk in the pod log and in the
// error the Job fails with, so the failure reads the same in both places.
func (s *scanner) walkFailed(step string, err error) error {
	s.logWalk(step, err)
	return fmt.Errorf("could not %s: %w", step, err)
}

// LogIncompleteWalk names why a walk pruned nothing and kept the
// counts it had: a root it could not read, or a count far below the
// catalog's.
func (s *scanner) logIncompleteWalk(readError bool, items, before int) {
	if readError {
		s.logf("incomplete walk: could not read the whole volume, keeping the last counts")
		return
	}
	s.logf("incomplete walk: read %d of %d cataloged items, keeping the last counts", items, before)
}

// logWalkComplete writes the one summary line a finished walk leaves:
// the counts, the sweep, and how long the walk took, then a capped sample of
// the folders it could not identify.
func (s *scanner) logWalkComplete(titles, unidentified, removed int, names []string, took time.Duration) {
	if removed >= 0 {
		s.logf("walk complete: %d titles, %d unidentified, %d removed, in %s", titles, unidentified, removed, took.Round(time.Millisecond))
	} else {
		s.logf("walk complete: %d titles, %d unidentified, prune deferred, in %s", titles, unidentified, took.Round(time.Millisecond))
	}
	if unidentified == 0 {
		return
	}
	if more := unidentified - len(names); more > 0 {
		s.logf("unidentified folders: %s, and %d more", strings.Join(names, ", "), more)
		return
	}
	s.logf("unidentified folders: %s", strings.Join(names, ", "))
}

// logf writes one scanner log line under the shared prefix, or nothing
// when the scanner was built without a log.
func (s *scanner) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	fmt.Fprintf(s.log, "library.liken.sh: "+format+"\n", args...)
}

// Rescan reads one title or series folder and reconciles the
// catalog to it, the answer to the folder a scan Job is given. It upserts what
// the folder holds and prunes only that folder's rows the re-read did not
// produce, such as a file an upgrade replaced. A folder that left the
// volume marks nothing, so all of its rows leave. It moves the last-change
// time when it wrote or removed a row, and leaves the counts and the
// last-walk time to the next full walk. A path that resolves to no folder
// falls back to a full walk.
func (s *scanner) rescan(ctx context.Context, absolute string) error {
	folder := s.titleFolderOf(absolute)
	if folder == "" {
		return s.fullWalk(ctx)
	}

	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	relative := relativePath(s.root, folder)
	if err := s.catalog.ensureSeen(ctx); err != nil {
		return s.walkFailed("ensure the seen table", err)
	}
	epoch := time.Now().UnixNano()
	result := &walkResult{}
	if dirExists(folder) {
		switch s.kind {
		case libraryKindMovies:
			scanMovieFolder(s.root, folder, s.library, result)
		case libraryKindSeries:
			scanSeriesFolder(s.root, folder, s.library, s.ignore, result)
		default:
			return nil
		}
	}

	// The sets this folder's movies named are read before the upsert, so a
	// movie that left its set still names the set that has to be derived
	// again. The set the folder names now is affected as well.
	var affected []string
	if s.kind == libraryKindMovies {
		held, err := s.catalog.setIDsUnder(ctx, s.library, relative)
		if err != nil {
			return s.walkFailed("read the sets of a rescan", err)
		}
		affected = append(held, setIDsOf(result.movies)...)
	}

	if err := flushWalk(ctx, s.catalog, result, epoch); err != nil {
		return s.walkFailed("write a rescan", err)
	}

	removed, err := pruneScope(ctx, s.catalog, s.library, relative, epoch)
	if err != nil {
		return s.walkFailed("prune a rescan", err)
	}

	// A rescan reads one folder and not a set's other members, so each
	// affected set derives again from the movie rows the catalog holds, after
	// the prune has taken the rows this folder lost.
	if err := reconcileSets(ctx, s.catalog, s.library, affected); err != nil {
		return s.walkFailed("write the sets of a rescan", err)
	}

	// A rescan moves the counts, so the Job's echo compares
	// against what the agent holds after it and never against a full
	// walk's counts.
	counts, err := s.catalog.countsOf(ctx, s.library)
	if err != nil {
		return s.walkFailed("count the catalog after a rescan", err)
	}
	s.mutex.Lock()
	s.counts = counts
	s.countsRead = true
	s.mutex.Unlock()

	written := len(result.movies) + len(result.series) + len(result.episodes) + len(result.files)
	if written == 0 && removed == 0 {
		s.logf("rescanned %s: no change", relative)
		return nil
	}
	s.logf("rescanned %s: wrote %d, removed %d", relative, written, removed)
	s.mutex.Lock()
	s.report.LastChange = time.Now().UTC()
	s.report.RemovedLastSweep = removed
	s.mutex.Unlock()
	return nil
}

// titleFolderOf maps a path on the volume to the title or series folder
// that holds it. A path outside the root maps to nothing.
//
// The movies rule is the walk's own rule: step down the path through the
// grouping folders, and stop at the first level that is a title folder or
// that has left the volume, no deeper than the walk's grouping cap. A
// level the scanner cannot read, and a path that names no title folder,
// both map to nothing, and the caller then walks the whole root. A series
// folder is always a child of the root.
func (s *scanner) titleFolderOf(absolute string) string {
	relative := relativePath(s.root, absolute)
	if relative == absolute || relative == "." || strings.HasPrefix(relative, "..") {
		return ""
	}
	parts := splitPath(relative)
	if len(parts) == 0 {
		return ""
	}
	if s.kind == libraryKindSeries {
		return path.Join(s.root, parts[0])
	}

	folder := s.root
	for depth, part := range parts {
		// The walk reads a title folder at one level past its grouping
		// cap, because it tests a directory for a title before it tests
		// the depth, so the resolver reaches the same level.
		if depth > movieGroupingDepth {
			return ""
		}
		folder = path.Join(folder, part)
		info, err := os.Stat(folder)
		// A folder that left the volume is the rescan's whole point: it
		// marks nothing, and every row under it leaves.
		if errors.Is(err, fs.ErrNotExist) {
			return folder
		}
		// A level the scanner cannot read is not a title folder that
		// left, and a prune scoped to it would delete rows the volume
		// still holds.
		if err != nil || !info.IsDir() {
			return ""
		}
		if isMovieTitleFolder(folder) {
			return folder
		}
	}
	return ""
}

// splitPath splits a relative path into its elements, dropping the
// empty ones a leading or doubled separator leaves.
func splitPath(relative string) []string {
	var parts []string
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}
