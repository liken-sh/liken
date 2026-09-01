package main

// The operator's loop has the shape liken's own operators use:
// level-triggered, woken by a watch, with a ticker as the backstop,
// and a reconcile before the first event ever arrives.
//
// A pass reads the whole collection instead of acting on the object an
// event carried. The event is only a wake. Every pass derives every
// status from what the API server and the report desk hold right now,
// so a lost event costs at most one backstop tick, a burst of events
// collapses into one pass, and a restarted operator starts correct
// with no replay.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The images the operator stamps into every scanner pod. Neither is
// discoverable from inside a pod, because which image a release ships
// is a decision the manifest carries, so the Deployment sets both and
// the operator refuses to start without them.
const (
	scannerImageVariable   = "SCANNER_IMAGE"
	corrosionImageVariable = "CORROSION_IMAGE"
)

// backstopInterval is how often the loop reconciles with nothing to
// prompt it. The tick recovers a lost watch event, and it is what
// notices a pod that changed phase while the watch was down.
const backstopInterval = 10 * time.Second

// passTimeout bounds every request one pass makes. The pass owns the
// context rather than taking the stop signal, so a shutdown lets the pass
// in flight finish its writes, and the loop returns on the next turn. A
// pass whose API server stops answering ends here, and the next pass
// starts clean. It is a variable so a test drives a short timeout.
var passTimeout = 30 * time.Second

// operator holds what every pass needs: the client it reads and writes
// through, the settings it stamps into each scanner pod, the bus, and
// the desk the bus folds each report onto. They are fields rather than
// globals so a test builds an operator around a desk and a cluster it
// controls.
type operator struct {
	client         *Client
	scannerImage   string
	corrosionImage string
	busAddress     string
	topicBase      string
	bus            *Bus
	reports        *reports

	// wake is the loop's own wake channel, and one channel serves the
	// two watches and the bus handler, because a wake says nothing
	// beyond "read the collection again".
	wake chan struct{}

	// The recreate backoff of each departing Library's cleanup pod,
	// keyed the way the report desk keys a Library, and dropped when
	// the Library goes.
	cleanupStands map[string]cleanupStand
}

// newOperator builds the operator and the two things it listens
// through: the desk that holds each Library's newest report, and the
// bus subscriptions that fill it. The subscriptions are remembered
// here and sent on every connection, so they outlive a broker
// restart.
func newOperator(client *Client, scannerImage, corrosionImage, busAddress, topicBase string) *operator {
	wake := make(chan struct{}, 1)
	library := &operator{
		client:         client,
		scannerImage:   scannerImage,
		corrosionImage: corrosionImage,
		busAddress:     busAddress,
		topicBase:      topicBase,
		reports:        newReports(wake),
		wake:           wake,
		cleanupStands:  map[string]cleanupStand{},
	}
	// The operator names no will. Its one publish is the empty
	// retained payload that drops a departed library's topics, and a
	// will replaces nothing about that: there is no message of the
	// operator's own that a broker should stand in for when the
	// connection breaks.
	library.bus = newBus(busAddress, "library-operator", nil, nil, library.handleBusMessage)
	library.bus.Subscribe(libraryStatusFilter(topicBase))
	library.bus.Subscribe(libraryAvailabilityFilter(topicBase))
	return library
}

// operate reads the operator's environment and returns its failure
// instead of exiting, so main is the only place that ends the process
// and a test drives the whole setup. A missing setting fails here,
// before the first pass, because a pod that cannot name the images it
// creates has nothing to reconcile with.
func operate() error {
	scannerImage := os.Getenv(scannerImageVariable)
	if scannerImage == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the scanner image", scannerImageVariable)
	}
	corrosionImage := os.Getenv(corrosionImageVariable)
	if corrosionImage == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the catalog image", corrosionImageVariable)
	}
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the broker", busAddressVariable)
	}
	// The topic base has a default, because a cluster that runs one
	// bus needs no policy for it.
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}

	client, err := InClusterClient()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}

	// The kubelet stops the pod with SIGTERM, and a person who runs
	// the binary by hand stops it with SIGINT. Both end the context,
	// and the process exits with a zero status.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	return newOperator(client, scannerImage, corrosionImage, busAddress, topicBase).run(stopped, os.Stdout)
}

// run is the operator without the process around it, so a test drives
// the whole loop against an API server it controls. It returns when
// the context ends, which is the stop signal.
func (o *operator) run(stopped context.Context, report io.Writer) error {
	go o.bus.Run(stopped)

	// The first lists do two jobs: they prove the operator can read
	// the collections it reconciles, and their resourceVersions are
	// where the watches start.
	startup, endStartup := context.WithTimeout(context.Background(), passTimeout)
	defer endStartup()

	libraries, err := ListLibraries(startup, o.client)
	if err != nil {
		return fmt.Errorf("listing libraries: %w", err)
	}
	catalogs, err := ListCatalogs(startup, o.client)
	if err != nil {
		return fmt.Errorf("listing catalogs: %w", err)
	}
	pods, err := ListScannerPods(startup, o.client)
	if err != nil {
		return fmt.Errorf("listing scanner pods: %w", err)
	}
	fmt.Fprintf(report, "library.liken.sh: operating %d libraries over %s\n",
		len(libraries.Items), o.busAddress)

	go watchLibraries(o.client, libraries.Metadata.ResourceVersion, o.wake)
	go watchCatalogs(o.client, catalogs.Metadata.ResourceVersion, o.wake)
	go watchPods(o.client, pods.Metadata.ResourceVersion, o.wake)

	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		o.pass()
		select {
		case <-stopped.Done():
			return nil
		case <-o.wake:
		case <-ticker.C:
		}
	}
}

// pass reconciles every Library in the cluster against its namespace's
// Catalog, then stands the catalog cluster of each namespace that holds
// a Catalog. That is the namespace's work and not one Library's. A
// failure on one object is reported and the pass continues, because one
// library's broken claim must not freeze every other library's status.
func (o *operator) pass() {
	ctx, done := context.WithTimeout(context.Background(), passTimeout)
	defer done()

	libraries, err := ListLibraries(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing libraries: %v\n", err)
		return
	}
	// The Catalog decides whether a Library proceeds, so the pass reads
	// the collection before it reconciles a Library, not after.
	catalogs, err := ListCatalogs(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing catalogs: %v\n", err)
		return
	}
	byNamespace := catalogsByNamespace(catalogs.Items)
	// The release rule reads every other Library in a namespace, so
	// the pass groups the survivors once for all of them.
	survivors := survivingLibraries(libraries.Items)

	live := make(map[string]bool, len(libraries.Items))
	for index := range libraries.Items {
		library := &libraries.Items[index]
		namespace, name := library.Metadata.Namespace, library.Metadata.Name
		live[libraryKey(namespace, name)] = true

		choice := singleCatalog(byNamespace[namespace])

		// A deleting Library takes the departure and never the
		// reconcile, because the reconcile would stand the scanner
		// back up to rewrite the rows the sweep is deleting.
		if library.Metadata.deleting() {
			if err := o.depart(ctx, library, survivors[namespace], choice); err != nil {
				fmt.Fprintf(os.Stderr, "departing library %s/%s: %v\n", namespace, name, err)
			}
			continue
		}

		if err := o.reconcile(ctx, library, choice); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling library %s/%s: %v\n", namespace, name, err)
		}
	}
	// The collection this pass read is the whole set of Libraries, so
	// anything else the desk holds belongs to a Library that is gone.
	// The pass clears the topics of every key it drops, because the
	// desk holds a key only while a retained message stands on the
	// bus: a scanner that died after its Library released leaves its
	// last will there, and a deletion from before this operator
	// cleared topics left both. Only a pass may clear one, because
	// the bus handler holds no Library list; the subscription
	// delivers the litter, the desk holds it, and the next pass
	// drops it.
	for _, key := range o.reports.retain(live) {
		namespace, name, _ := strings.Cut(key, "/")
		o.clearLibraryTopics(namespace, name)
	}
	for key := range o.cleanupStands {
		if !live[key] {
			delete(o.cleanupStands, key)
		}
	}

	o.reconcileCatalogs(ctx, byNamespace)
}

// handleBusMessage folds one message from the broker onto the report
// desk. It runs on the bus reader's goroutine, so it does nothing
// beyond the fold, and the wake the desk raises is what carries the
// message into the next pass.
func (o *operator) handleBusMessage(topic string, payload []byte) {
	namespace, name, kind, ok := parseLibraryTopic(o.topicBase, topic)
	if !ok {
		return
	}
	// An empty payload is how a retained topic is cleared, so it
	// carries nothing to fold, for either kind. The rule must cover
	// availability too: the operator subscribes to the topics it
	// clears, so its own clears come back to it, and folding one as
	// offline would put back the desk state the pass just dropped.
	// The next pass would clear again, and the two would trade
	// messages forever. A real scanner only ever publishes online or
	// offline, so nothing real is dropped here.
	if len(payload) == 0 {
		return
	}
	switch kind {
	case libraryStatusKind:
		var report libraryReport
		if err := json.Unmarshal(payload, &report); err != nil {
			fmt.Fprintf(os.Stderr, "reading the report on %s: %v\n", topic, err)
			return
		}
		o.reports.fold(namespace, name, report)
	case libraryAvailabilityKind:
		o.reports.availability(namespace, name, string(payload) == availabilityOnline)
	}
}
