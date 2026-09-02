package main

// The reporter is the container beside the standing catalog agent. It
// is the one process in the namespace that reads the catalog and
// publishes what it holds: one retained report per library, rebuilt
// whenever the runs table changes. It holds no Kubernetes credentials,
// it answers on no port, and it never exits on its own, because every
// Job in the namespace waits on this process to echo its run.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
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
var reportReadTimeout = 30 * time.Second

// The wait before the reporter opens the run stream again after it
// ends. It doubles up to the ceiling, so an agent that is down does not
// become a tight loop. Variables, so a test drives a reconnect in
// milliseconds.
var (
	reportMinBackoff = time.Second
	reportMaxBackoff = 30 * time.Second
)

// How long the reporter holds the bus open after it publishes the
// closing offline, so the writer goroutine sends it before the process
// exits. A variable, so a test drives a shutdown in milliseconds.
var reportFlushGrace = 500 * time.Millisecond

// One reporter: the namespace it serves, the catalog it reads, the bus
// it publishes on, and the report it last published per library.
type reporter struct {
	namespace         string
	topicBase         string
	availabilityTopic string
	catalog           *Catalog
	bus               *Bus
	log               io.Writer

	mutex     sync.Mutex
	published map[string]libraryReport
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
	report.bus = newBus(os.Getenv(busAddressVariable), "catalog-"+namespace,
		&busWill{Topic: report.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		report.onConnect, nil)
	return report
}

// serve holds the bus and the run stream open until the context ends,
// then marks this reporter offline and returns. The bus runs on a
// context of its own, so the closing publish has a live connection to
// go out on.
func (r *reporter) serve(stopped context.Context) {
	running, stopBus := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.bus.Run(running)
	}()

	r.follow(stopped)

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
	if !ok {
		return
	}
	read, cancel := context.WithTimeout(ctx, reportReadTimeout)
	defer cancel()

	report, err := r.buildReport(read, library)
	if err != nil {
		r.logf("could not build the report of %s: %v", library, err)
		return
	}
	r.mutex.Lock()
	r.published[library] = report
	r.mutex.Unlock()

	payload, _ := json.Marshal(report)
	r.bus.Publish(libraryStatusTopic(r.topicBase, namespace, name), payload, true)
}

// buildReport reads one library's counts and runs. The scan run is
// what says when the volume was last walked, how many folders the walk
// could not identify, how many rows its prune took, and whether a walk
// runs now, which is a scan run that started after it last finished.
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

	report := libraryReport{Titles: titles, Items: items, Files: files, Runs: runs[library]}
	if walk, held := runOf(report.Runs, workerScan); held {
		report.LastWalk = walk.Finished
		report.Unidentified = walk.Unidentified
		report.RemovedLastSweep = walk.Removed
		report.Walking = walk.Started.After(walk.Finished)
	}
	report.LastChange = r.lastChange(library, report)
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
