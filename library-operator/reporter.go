package main

// The reporter is the container beside the standing catalog agent. It
// is the one process in the namespace that reads the catalog and
// publishes what it holds: one retained report per library, rebuilt
// whenever the runs table changes, while any replicated table keeps
// changing, and when the operator publishes new refresh times for a
// library. It holds no Kubernetes credentials, it answers on no
// port, and it never exits on its own, because the operator reads a
// Library's whole status off this process.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The argument that selects this role, the way scanMode selects the
// scanner. The operator writes it over the image's entrypoint.
const reportMode = "report"

// reportReadTimeout bounds one read of the catalog, so a stuck agent
// cannot hold the reporter in a query forever.
const reportReadTimeout = 30 * time.Second

// The wait before the reporter opens the run stream again after it
// ends. It doubles up to the ceiling, so an agent that is down does not
// become a tight loop.
const (
	reportMinBackoff = time.Second
	reportMaxBackoff = 30 * time.Second
)

// How long a run of changes waits before the reporter
// republishes again, so a walk that writes thousands of rows costs one
// report a second and not one report a row.
const reportDebounce = time.Second

// How long the reporter waits for its copy of the catalog to hold the write
// that the broker's report of a library names, before it reports what the
// copy holds. The copy of a catalog pod that just started syncs with its
// peers in seconds. The bound is for a catalog that was built again, which
// never holds a write of the catalog before it, and it is the time a Job
// gives its own copy.
const reportSyncBound = defaultSyncTimeout

// How long the reporter holds the bus open after it publishes the
// closing offline, so the writer goroutine sends it before the process
// exits.
const reportFlushGrace = 500 * time.Millisecond

// One reporter: the namespace it serves, the catalog it reads, the bus
// it publishes on, and the report it last published per library.
type reporter struct {
	namespace         string
	topicBase         string
	availabilityTopic string
	catalog           *Catalog
	bus               *Bus
	log               io.Writer

	// Reads the message the broker retains on one topic, and whether it
	// retains one.
	retained func(ctx context.Context, topic string) ([]byte, bool, error)

	mutex     sync.Mutex
	published map[string]libraryReport
	// The libraries this process has published no report of yet, because
	// its copy of the catalog may be behind the report the broker retains.
	waiting map[string]reportWait
	// The refresh and retry times of each Library, by library key, as the
	// operator last published them. A Library with no entry counts its gaps
	// with neither.
	refresh map[string]reopenTimes
	// The mark that a Library's refresh times changed, which the bus handler
	// raises and the republish loop answers, because the handler runs on the
	// bus reader and must not wait on a catalog read.
	changed chan struct{}
}

// The wait of one library's first report: the write the copy must hold,
// which is the newest confirmed run that the broker's report names, whether
// the broker's report was read, when the wait began, and when the reporter
// looks at the copy again.
type reportWait struct {
	target  syncTarget
	read    bool
	since   time.Time
	recheck time.Time
}

// runReport is the report role's whole program: read the environment,
// publish until the kubelet stops the container, and mark the reporter
// offline on the way out.
func runReport() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	newReporter(os.Stdout).serve(stopped)
}

// newReporter reads the namespace, the bus, and the agent's address
// out of the container's environment, the only place a pod with no API
// credential learns them.
func newReporter(log io.Writer) *reporter {
	namespace := os.Getenv(libraryNamespaceVariable)
	base := os.Getenv(topicBaseVariable)
	if base == "" {
		base = defaultTopicBase
	}
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}

	fmt.Fprintf(log, "library.liken.sh: reporting the catalog of %s from %s\n", namespace, api)

	report := &reporter{
		namespace:         namespace,
		topicBase:         base,
		availabilityTopic: catalogAvailabilityTopic(base, namespace),
		// No client timeout, because the run stream stays open for the
		// life of the pod. Every read bounds itself with a context instead.
		catalog:   NewCatalog(api, &http.Client{}),
		log:       log,
		published: map[string]libraryReport{},
	}
	endpoint := busEndpointOf(os.Getenv(busAddressVariable), base)
	report.retained = func(ctx context.Context, topic string) ([]byte, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, workItemTimeout)
		defer cancel()
		session, err := openBusSession(ctx, endpoint.dial, "catalog-"+namespace+"-retained")
		if err != nil {
			return nil, false, err
		}
		defer session.close()
		return session.readRetained(topic)
	}
	report.bus = newBus(os.Getenv(busAddressVariable), "catalog-"+namespace,
		&busWill{Topic: report.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		report.onConnect, report.handleBusMessage)
	return report
}

// Serve holds the bus, the run stream, and one update stream
// per replicated table open until the context ends, then marks this
// reporter offline and returns. The bus runs on a context of its own,
// so the closing publish has a live connection to go out on.
func (r *reporter) serve(stopped context.Context) {
	r.mutex.Lock()
	if r.changed == nil {
		r.changed = make(chan struct{}, 1)
	}
	changed := r.changed
	r.mutex.Unlock()
	r.bus.Subscribe(libraryRefreshFilter(r.topicBase, r.namespace))

	running, stopBus := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.bus.Run(running)
	}()

	// The runs stream carries the values a report needs. The update
	// streams carry only the fact that a row moved, so they mark a
	// change, and the republish reads the catalog. A refresh time that
	// changes on the bus raises the same mark.
	var streams sync.WaitGroup
	for _, table := range catalogTables {
		streams.Add(1)
		go func() {
			defer streams.Done()
			r.followTable(stopped, table, changed)
		}()
	}
	streams.Add(1)
	go func() {
		defer streams.Done()
		r.republishWhileChanging(stopped, changed)
	}()

	r.follow(stopped)
	streams.Wait()

	r.bus.Publish(r.availabilityTopic, []byte(availabilityOffline), true)
	time.Sleep(reportFlushGrace)
	stopBus()
	<-done
}

// follow publishes every library the catalog holds, then follows the
// runs table until the stream ends, and starts over after a backoff.
// Nothing the catalog answers ends this loop. Only the context does.
func (r *reporter) follow(ctx context.Context) {
	backoff := reportMinBackoff
	for ctx.Err() == nil {
		r.publishEveryLibrary(ctx)

		reached := false
		err := r.catalog.subscribeRuns(ctx, func() { reached = true }, func(library string) {
			r.publishLibrary(ctx, library)
		})
		if err != nil && ctx.Err() == nil {
			r.logf("the run stream ended: %v", err)
		}
		if reached {
			backoff = reportMinBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !reached {
			backoff = min(backoff*2, reportMaxBackoff)
		}
	}
}

// Follows one table's update stream for the life of the context.
func (r *reporter) followTable(ctx context.Context, table string, changed chan<- struct{}) {
	followTableChanges(ctx, r.catalog, table, changed, r.logf)
}

// Republishes every library the reporter knows on each marked
// change, then holds off for the debounce, so a run of changes costs
// one report per library per interval and one more after they stop.
func (r *reporter) republishWhileChanging(ctx context.Context, changed <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}

		r.publishKnownLibraries(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(reportDebounce):
		}
	}
}

// The mark never blocks and never queues more than one, because
// the republish that answers it reads every library whole.
func markChanged(changed chan<- struct{}) {
	select {
	case changed <- struct{}{}:
	default:
	}
}

// Publishes a report for every library the reporter has already
// published one for, which is every library the catalog held when the
// reporter started and every library a run has named since, and for
// every library whose first report waits for the copy.
func (r *reporter) publishKnownLibraries(ctx context.Context) {
	r.mutex.Lock()
	libraries := make([]string, 0, len(r.published)+len(r.waiting))
	for library := range r.published {
		libraries = append(libraries, library)
	}
	for library := range r.waiting {
		libraries = append(libraries, library)
	}
	r.mutex.Unlock()

	slices.Sort(libraries)
	for _, library := range libraries {
		r.publishLibrary(ctx, library)
	}
}

// publishEveryLibrary publishes one report for every library the
// catalog holds rows for. This is how a reporter that has just started
// fills the broker before the first run lands.
func (r *reporter) publishEveryLibrary(ctx context.Context) {
	read, cancel := context.WithTimeout(ctx, reportReadTimeout)
	defer cancel()

	libraries, err := r.catalog.LibraryKeys(read)
	if err != nil {
		r.logf("could not read the libraries the catalog holds: %v", err)
		return
	}
	for _, library := range libraries {
		r.publishLibrary(ctx, library)
	}
}

// publishLibrary builds one library's report from the catalog and
// publishes it retained. A read that fails leaves the retained report
// where it is, so an agent that stops answering never reads as an
// empty library.
func (r *reporter) publishLibrary(ctx context.Context, library string) {
	namespace, name, ok := splitLibraryKey(library)
	// A reporter that is stopping publishes no more, so a library that
	// waits for the copy stops looking at it.
	if !ok || ctx.Err() != nil {
		return
	}
	read, cancel := context.WithTimeout(ctx, reportReadTimeout)
	defer cancel()

	topic := libraryStatusTopic(r.topicBase, namespace, name)
	if !r.copyReached(read, library, topic, func() { r.publishLibrary(ctx, library) }) {
		return
	}
	report, err := r.buildReport(read, library)
	if err != nil {
		r.logf("could not build the report of %s: %v", library, err)
		return
	}
	r.mutex.Lock()
	r.published[library] = report
	delete(r.waiting, library)
	r.mutex.Unlock()

	payload, _ := json.Marshal(report)
	r.bus.Publish(topic, payload, true)
}

// Whether this process may publish a report of one library. It may once it
// has published one, and before that once its copy of the catalog holds the
// newest confirmed run that the broker's report of the library names.
//
// A catalog pod can start on a copy that is behind its peers: each node
// keeps a copy of its own, and a pod that starts on another node than
// before reads that node's older copy until it has synced. The broker
// retains the last report of the reporter before this one, which was true.
// A report from the older copy would replace it with counts that are too
// low, and the operator would start Jobs to fill gaps that are not open
// and to place end titles that are placed. So the first report of a
// library waits for the copy, and the broker's report stands until then.
//
// While a library waits, the reporter looks at the copy again once a
// second, as a Job that waits for its own copy does, because a sync that
// brings only the versions of another writer changes no table the reporter
// follows. again is that look: the publish of this one library, so the
// wait of one library builds no report of another. A library the broker
// holds no report of waits for nothing. After reportSyncBound the reporter
// publishes what the copy holds and says so.
func (r *reporter) copyReached(ctx context.Context, library, topic string, again func()) bool {
	now := time.Now()
	r.mutex.Lock()
	_, published := r.published[library]
	wait, waiting := r.waiting[library]
	r.mutex.Unlock()
	if published {
		return true
	}
	if !waiting {
		wait = reportWait{since: now}
	}
	if !wait.read {
		payload, held, err := r.retained(ctx, topic)
		if err != nil {
			r.logf("could not read the report the broker retains of %s: %v", library, err)
		} else {
			wait.read = true
			var before libraryReport
			if held && json.Unmarshal(payload, &before) == nil {
				wait.target = syncTargetFor(before.Runs)
			}
		}
	}
	if wait.read {
		synced, err := catalogSynced(ctx, r.catalog, wait.target)
		if err != nil {
			r.logf("could not read how far the copy of %s is: %v", library, err)
		}
		if synced {
			return true
		}
	}
	if now.Sub(wait.since) >= reportSyncBound {
		r.logf("reporting %s from a copy that did not reach version %d of %s within %s",
			library, wait.target.version, wait.target.actor, reportSyncBound)
		return true
	}
	if !now.Before(wait.recheck) {
		wait.recheck = now.Add(catalogSyncInterval)
		time.AfterFunc(catalogSyncInterval, again)
	}
	r.mutex.Lock()
	if r.waiting == nil {
		r.waiting = map[string]reportWait{}
	}
	r.waiting[library] = wait
	r.mutex.Unlock()
	return false
}

// buildReport reads one library's counts, runs, and gaps. The scan run is
// what says when the volume was last walked, how many folders the walk
// could not identify, how many rows its prune took, and whether a walk
// runs now, which is a scan run that started after it last finished. The
// gaps come from the same queries the enricher containers work from, so
// the count the operator schedules on is the count of rows a container
// finds.
//
// The tallies come last. They are in the report because the operator is the
// one process Prometheus scrapes and the containers that wrote them have
// exited.
func (r *reporter) buildReport(ctx context.Context, library string) (libraryReport, error) {
	runs, err := r.catalog.Runs(ctx)
	if err != nil {
		return libraryReport{}, err
	}
	titles, err := r.catalog.countTitles(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	items, err := r.catalog.countItems(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	files, err := r.catalog.countFiles(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	itemsByKind, err := r.catalog.countItemsByKind(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}

	report := libraryReport{Titles: titles, Items: items, Files: files, ItemsByKind: itemsByKind, Runs: runs[library]}
	if walk, held := runOf(report.Runs, workerScan); held {
		report.LastWalk = walk.Finished
		report.Unidentified = walk.Unidentified
		report.RemovedLastSweep = walk.Removed
		report.Walking = walk.Started.After(walk.Finished)
	}
	report.LastChange = r.lastChange(library, report)

	refresh := r.refreshOf(library)
	gaps, err := r.catalog.gapCounts(ctx, library, time.Now().UTC(), refresh)
	if err != nil {
		return libraryReport{}, err
	}
	report.Gaps = gaps
	report.EpisodeGaps, err = r.catalog.episodeGapCounts(ctx, library, time.Now().UTC(), refresh)
	if err != nil {
		return libraryReport{}, err
	}
	report.OldestAttempts, report.OldestMisses, err = r.catalog.oldestAttempts(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	report.Waiting, report.Unresolved, err = r.catalog.identityCounts(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	report.Fights, err = r.catalog.fightCount(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	report.TitlesMarked, err = r.catalog.titlesMarked(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	report.TitlesReadings, err = r.catalog.titlesReadings(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	report.Tallies, err = r.catalog.Tallies(ctx, library)
	if err != nil {
		return libraryReport{}, err
	}
	return report, nil
}

// lastChange is the time this library's counts last moved. The
// reporter compares each report with the one it published before, so a
// report that counts the same rows carries the same time. A reporter
// that has just started has no earlier report, and takes the last walk.
func (r *reporter) lastChange(library string, report libraryReport) time.Time {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	previous, held := r.published[library]
	if !held {
		return report.LastWalk
	}
	if previous.Titles == report.Titles && previous.Items == report.Items && previous.Files == report.Files {
		return previous.LastChange
	}
	return time.Now().UTC()
}

// onConnect refills the broker the moment a session connects, because
// a broker that restarts drops its retained messages.
func (r *reporter) onConnect(bus *Bus) {
	bus.Publish(r.availabilityTopic, []byte(availabilityOnline), true)
	r.mutex.Lock()
	held := make(map[string]libraryReport, len(r.published))
	for library, report := range r.published {
		held[library] = report
	}
	r.mutex.Unlock()
	for library, report := range held {
		namespace, name, ok := splitLibraryKey(library)
		if !ok {
			continue
		}
		payload, _ := json.Marshal(report)
		bus.Publish(libraryStatusTopic(r.topicBase, namespace, name), payload, true)
	}
}

// handleBusMessage folds one Library's refresh and retry times, which the operator
// publishes retained. An empty payload clears them, which is what the
// operator publishes when the Library departs. The fold raises the change
// mark, so the republish loop counts the gaps again.
func (r *reporter) handleBusMessage(topic string, payload []byte) {
	namespace, name, kind, ok := parseLibraryTopic(r.topicBase, topic)
	if !ok || kind != libraryRefreshKind || namespace != r.namespace {
		return
	}
	library := libraryKey(namespace, name)
	r.mutex.Lock()
	if r.refresh == nil {
		r.refresh = map[string]reopenTimes{}
	}
	if len(payload) == 0 {
		delete(r.refresh, library)
	} else {
		r.refresh[library] = parseReopenTimes(string(payload))
	}
	changed := r.changed
	r.mutex.Unlock()
	if changed != nil {
		markChanged(changed)
	}
}

// The refresh and retry times of one Library, and none where the operator
// has published none.
func (r *reporter) refreshOf(library string) reopenTimes {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.refresh[library]
}

// splitLibraryKey reads a library key back into the namespace and the
// name that libraryKey joined.
func splitLibraryKey(library string) (namespace, name string, ok bool) {
	namespace, name, found := strings.Cut(library, "/")
	if !found || namespace == "" || name == "" {
		return "", "", false
	}
	return namespace, name, true
}

// logf writes one line under the shared prefix, or nothing when the
// reporter was built without a log.
func (r *reporter) logf(format string, args ...any) {
	if r.log == nil {
		return
	}
	fmt.Fprintf(r.log, "library.liken.sh: "+format+"\n", args...)
}
