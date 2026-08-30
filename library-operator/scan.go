package main

// The scanner is the container that walks one library's volume and
// reports what it holds. It runs beside a Corrosion agent in the pod
// the operator creates for each Library, it mounts the claim read
// only, and it holds no Kubernetes credentials: every fact it reports
// reaches the control plane over the bus, and the operator alone
// writes a Library's status. It writes the catalog only through its
// own agent's transaction API, on the pod's loopback.
//
// The scanner detects a change two ways. A webhook of the kind Radarr,
// Sonarr, and Jellyfin send rescans one path at once, and a slow timer
// re-walks the whole root. It does not use inotify, which fires only
// for writes made through the same kernel and never for another
// client's writes to a network volume.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net"
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

// The address the webhook listens on. The drill reaches this port on
// the pod directly, so the plan adds no Service. A var so a test binds
// an ephemeral port.
var webhookAddress = ":8090"

// The wait between full walks. The walk is how a file that arrived with
// no webhook reaches the catalog. A var so a test drives several walks
// in a second.
var scanInterval = 5 * time.Minute

// scanFlushGrace is how long the scanner holds the bus open after it
// publishes the closing offline, so the writer goroutine drains it
// before the process exits. The message is QoS 0 and carries no
// acknowledgement, so this window is the only signal the scanner has
// that it left. It is a variable so a test drives a shutdown in
// milliseconds.
var scanFlushGrace = 500 * time.Millisecond

// catalogWriteTimeout bounds a walk's writes to the catalog agent, so a
// stuck agent cannot hold a walk open forever.
var catalogWriteTimeout = 2 * time.Minute

// scanner is one scanner container: the two topics it publishes, the
// mutable report it stands behind, the catalog it writes, and the root
// it walks. The report changes across walks, so a mutex covers it.
type scanner struct {
	statusTopic       string
	availabilityTopic string
	root              string
	library           string
	kind              string
	ignore            ignoreSet
	catalog           *Catalog
	bus               *Bus

	mutex  sync.Mutex
	report libraryReport

	// walkMutex serializes the walks. A timer walk and a webhook rescan
	// can arrive at once, and both write the catalog and reconcile it
	// against the volume. One walk runs at a time, so the reconciliation
	// reads a settled catalog.
	walkMutex sync.Mutex

	webhookAddr string
}

// runScan is the scan role's whole program. It reads the environment,
// connects to the bus, and holds the pod open until the kubelet stops
// it.
func runScan() {
	// The kernel runs no default action for a signal sent to PID 1, and
	// the scanner is its container's PID 1. The signal context is what
	// ends the wait below, on the kubelet's SIGTERM or on the interrupt
	// a person who runs the binary by hand sends.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	newScanner(time.Now().UTC(), os.Stdout).serve(stopped)
}

// newScanner reads the container's environment and builds the client
// that speaks for this Library. started is the time the report carries
// before the first walk: a scanner that has walked nothing has read
// the volume as of the moment it came up.
func newScanner(started time.Time, log io.Writer) *scanner {
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
		statusTopic:       libraryStatusTopic(base, namespace, name),
		availabilityTopic: libraryAvailabilityTopic(base, namespace, name),
		root:              mountRoot,
		library:           libraryKey(namespace, name),
		kind:              kind,
		ignore:            ignore,
		catalog:           NewCatalog(api, &http.Client{Timeout: catalogWriteTimeout}),
		report:            libraryReport{LastWalk: started, LastChange: started},
	}
	// The will is what marks the scanner offline when the pod dies
	// without a chance to publish, which is every kill the kubelet does
	// not ask for first.
	scan.bus = newBus(os.Getenv(busAddressVariable), "library-"+namespace+"-"+name,
		&busWill{Topic: scan.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		scan.onConnect, nil)
	return scan
}

// serve runs one full walk, then holds the bus, the webhook, and the
// slow timer open until the context ends, then marks this scanner
// offline and returns. The first walk runs before the bus connects, so
// the first report the broker holds already carries the counts. The bus
// runs on its own context, so the closing publish has a live connection
// to leave on. The retained report stays where it is: a library
// outlives its scanner.
func (s *scanner) serve(stopped context.Context) {
	running, stopBus := context.WithCancel(context.Background())

	s.fullWalk(running)

	go s.bus.Run(running)
	server := s.startWebhook()
	timerDone := make(chan struct{})
	go func() {
		defer close(timerDone)
		s.runTimer(running)
	}()

	<-stopped.Done()

	// Shutting the webhook down drains its in-flight rescans, and the
	// cancel ends the timer, so no walk runs after serve returns.
	s.stopWebhook(server)
	s.bus.Publish(s.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(scanFlushGrace)
	stopBus()
	<-timerDone
}

// startWebhook opens the import endpoint. A bind that fails leaves the
// scanner without a webhook, still walking on the timer.
func (s *scanner) startWebhook() *http.Server {
	listener, err := net.Listen("tcp", webhookAddress)
	if err != nil {
		return nil
	}
	s.webhookAddr = listener.Addr().String()
	server := &http.Server{Handler: s.webhookHandler()}
	go server.Serve(listener)
	return server
}

// stopWebhook closes the import endpoint within the flush grace, so a
// slow shutdown does not hold the process open.
func (s *scanner) stopWebhook(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scanFlushGrace)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// runTimer re-walks the whole root on the slow interval until the
// context ends. This is the path a file that arrived with no webhook
// takes into the catalog.
func (s *scanner) runTimer(ctx context.Context) {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fullWalk(ctx)
		}
	}
}

// walkFolders streams the title folders for this library's kind, one at
// a time, so a full walk holds one folder and never the whole library.
// An unknown kind streams nothing and reports zero titles rather than
// failing. A cancelled context stops the stream between folders, so a
// walk of a large volume does not run on past a shutdown.
func (s *scanner) walkFolders(ctx context.Context) iter.Seq[*walkResult] {
	var folders iter.Seq[*walkResult]
	switch s.kind {
	case libraryKindMovies:
		folders = walkMovieFolders(s.root, s.library, s.ignore)
	case libraryKindSeries:
		folders = walkSeriesFolders(s.root, s.library, s.ignore)
	default:
		return func(yield func(*walkResult) bool) {}
	}
	return func(yield func(*walkResult) bool) {
		for folder := range folders {
			if ctx.Err() != nil {
				return
			}
			if !yield(folder) {
				return
			}
		}
	}
}

// fullWalk streams the root one title folder at a time, buffers the rows
// until the buffer reaches scanFlushBatch, and flushes each buffer:
// it upserts the rows and marks them with the walk's epoch. It then prunes
// the rows the walk did not mark. It updates the counts and the last-walk
// time, and moves the last-change time only when the catalog changed. A
// write that fails leaves the catalog as it was, so the next walk retries.
// An incomplete walk keeps its prune for the next clean walk, so a partial
// read never mass-deletes.
func (s *scanner) fullWalk(ctx context.Context) {
	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	if err := s.catalog.ensureSeen(ctx); err != nil {
		return
	}
	epoch := time.Now().UnixNano()

	before, err := s.catalog.countItems(ctx, s.library)
	if err != nil {
		return
	}

	buffer := &walkResult{}
	buffered, items, titles, unidentified := 0, 0, 0, 0
	readError := false
	flush := func() bool {
		if buffered == 0 {
			return true
		}
		if err := flushWalk(ctx, s.catalog, buffer, epoch); err != nil {
			return false
		}
		buffer = &walkResult{}
		buffered = 0
		return true
	}

	for folder := range s.walkFolders(ctx) {
		if folder.readError {
			readError = true
		}
		appendFolder(buffer, folder)
		found := len(folder.movies) + len(folder.series) + len(folder.episodes)
		items += found
		buffered += found
		titles += folder.titles
		unidentified += folder.unidentified
		if buffered >= scanFlushBatch && !flush() {
			return
		}
	}
	if !flush() {
		return
	}

	removed := -1
	if !incompleteWalk(readError, items, before) {
		count, err := pruneLibrary(ctx, s.catalog, s.library, epoch)
		if err != nil {
			return
		}
		removed = count
	}

	after, err := s.catalog.countItems(ctx, s.library)
	if err != nil {
		return
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
	s.mutex.Unlock()
	s.publishReport()
}

// rescan reads one title or series folder and reconciles the catalog to
// it, the answer to a webhook that names an imported path. It upserts what
// the folder holds and prunes only that folder's rows the re-read did not
// produce, such as a file an upgrade replaced. A folder that left the
// volume marks nothing, so all of its rows leave. It moves the last-change
// time when it wrote or removed a row, and leaves the counts and the
// last-walk time to the next full walk. A path that resolves to no folder
// falls back to a full walk.
func (s *scanner) rescan(ctx context.Context, absolute string) {
	folder := s.titleFolderOf(absolute)
	if folder == "" {
		s.fullWalk(ctx)
		return
	}

	s.walkMutex.Lock()
	defer s.walkMutex.Unlock()

	if err := s.catalog.ensureSeen(ctx); err != nil {
		return
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
			return
		}
	}

	if err := flushWalk(ctx, s.catalog, result, epoch); err != nil {
		return
	}

	removed, err := pruneScope(ctx, s.catalog, s.library, relativePath(s.root, folder), epoch)
	if err != nil {
		return
	}

	wrote := len(result.movies)+len(result.series)+len(result.episodes)+len(result.files) > 0
	if !wrote && removed == 0 {
		return
	}
	s.mutex.Lock()
	s.report.LastChange = time.Now().UTC()
	s.mutex.Unlock()
	s.publishReport()
}

// titleFolderOf maps a path on the volume to the title or series folder
// that holds it. A movies title folder is a child of the root or of a
// grouping folder one level down; a series folder is always a child of
// the root. A path outside the root maps to nothing.
func (s *scanner) titleFolderOf(absolute string) string {
	relative := relativePath(s.root, absolute)
	if relative == absolute || relative == "." || strings.HasPrefix(relative, "..") {
		return ""
	}
	parts := splitPath(relative)
	if len(parts) == 0 {
		return ""
	}
	first := path.Join(s.root, parts[0])
	if s.kind == libraryKindSeries {
		return first
	}
	if isMovieTitleFolder(first) {
		return first
	}
	if len(parts) >= 2 {
		return path.Join(first, parts[1])
	}
	return first
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

// publishReport writes the current report to the bus, retained. A
// disconnected bus drops the publish, and onConnect republishes it.
func (s *scanner) publishReport() {
	s.mutex.Lock()
	payload, _ := json.Marshal(s.report)
	s.mutex.Unlock()
	s.bus.Publish(s.statusTopic, payload, true)
}

// onConnect refills the broker the moment a session reaches a CONNACK.
// It publishes online and republishes the report, because a broker
// that restarts drops its retained set, and a reconnect has to leave
// the current report behind again.
func (s *scanner) onConnect(bus *Bus) {
	bus.Publish(s.availabilityTopic, []byte(availabilityOnline), true)
	s.mutex.Lock()
	payload, _ := json.Marshal(s.report)
	s.mutex.Unlock()
	bus.Publish(s.statusTopic, payload, true)
}
