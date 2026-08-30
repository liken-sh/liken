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
	"maps"
	"os"
	"os/signal"
	"slices"
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
	}
	// The operator publishes nothing, so it names no will: it is a
	// reader of every scanner's topics and no scanner reads a topic of
	// its own.
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
	libraries, err := ListLibraries(o.client)
	if err != nil {
		return fmt.Errorf("listing libraries: %w", err)
	}
	pods, err := ListScannerPods(o.client)
	if err != nil {
		return fmt.Errorf("listing scanner pods: %w", err)
	}
	fmt.Fprintf(report, "library.liken.sh: operating %d libraries over %s\n",
		len(libraries.Items), o.busAddress)

	go watchLibraries(o.client, libraries.Metadata.ResourceVersion, o.wake)
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

// pass reconciles every Library in the cluster, then stands the
// catalog cluster of each namespace that holds one. That is the
// namespace's work and not one Library's. A failure on one object is
// reported and the pass continues, because one library's broken claim
// must not freeze every other library's status.
func (o *operator) pass() {
	list, err := ListLibraries(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing libraries: %v\n", err)
		return
	}
	live := make(map[string]bool, len(list.Items))
	for index := range list.Items {
		library := &list.Items[index]
		live[libraryKey(library.Metadata.Namespace, library.Metadata.Name)] = true
		if err := o.reconcile(library); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling library %s/%s: %v\n",
				library.Metadata.Namespace, library.Metadata.Name, err)
		}
	}
	// The collection this pass read is the whole set of Libraries, so
	// anything else the desk holds belongs to a Library that is gone.
	o.reports.retain(live)

	o.standCatalogs(list.Items)
}

// standCatalogs gives each namespace's catalog cluster its Service
// and its peers: one headless Service and one EndpointSlice in every
// namespace that holds a Library, over that namespace's scanner pods.
// The pods come from one cluster-wide list, because a Library can be
// in any namespace. A failure in one namespace is reported and the
// rest still stand, and the statuses this pass wrote stand whatever
// the objects say. The pod watch already wakes the loop when a pod
// changes, so a new pod's address reaches its slice on the next pass.
func (o *operator) standCatalogs(libraries []Library) {
	pods, err := ListScannerPods(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing scanner pods: %v\n", err)
		return
	}
	owners := catalogOwners(libraries)
	for _, namespace := range slices.Sorted(maps.Keys(owners)) {
		if err := o.standCatalogService(namespace, owners[namespace]); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog service in %s: %v\n", namespace, err)
		}
		if err := o.standCatalogEndpoints(namespace, owners[namespace], pods.Items); err != nil {
			fmt.Fprintf(os.Stderr, "standing the catalog endpoints in %s: %v\n", namespace, err)
		}
	}
}

// catalogOwners groups every Library by its namespace, as the
// ownerReferences the catalog objects of that namespace carry. Each
// namespace's list is sorted by name, so two passes build the same
// object and the divergence check reads no reorder as a change. None
// of the references is the controller, because an object with several
// owners has no single one.
func catalogOwners(libraries []Library) map[string][]OwnerReference {
	owners := map[string][]OwnerReference{}
	for index := range libraries {
		library := &libraries[index]
		owners[library.Metadata.Namespace] = append(owners[library.Metadata.Namespace], OwnerReference{
			APIVersion: libraryAPIVersion,
			Kind:       "Library",
			Name:       library.Metadata.Name,
			UID:        library.Metadata.UID,
		})
	}
	for namespace := range owners {
		slices.SortFunc(owners[namespace], func(one, other OwnerReference) int {
			return strings.Compare(one.Name, other.Name)
		})
	}
	return owners
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
	switch kind {
	case libraryStatusKind:
		// An empty payload is how a retained topic is cleared, so it
		// carries no report to fold.
		if len(payload) == 0 {
			return
		}
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
