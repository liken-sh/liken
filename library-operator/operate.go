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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The images the operator stamps into every pod it creates. Neither is
// discoverable from inside a pod, because which image a release ships
// is a decision the manifest carries, so the Deployment sets both and
// the operator refuses to start without them.
const (
	scannerImageVariable   = "SCANNER_IMAGE"
	corrosionImageVariable = "CORROSION_IMAGE"
	browserImageVariable   = "BROWSER_IMAGE"
)

// The name this operator answers to as an idle controller. A Player
// whose status.idle.controller reads this gets a screen pod, and no other
// Player does. media-operator writes the name; this operator only compares it.
const screenController = "library.liken.sh/media-browser"

// BackstopInterval is how often the loop reconciles with nothing to
// prompt it. The tick recovers a lost watch event, and it is what
// notices a pod that changed phase while the watch was down.
const backstopInterval = 10 * time.Second

// PassTimeout bounds every request one pass makes. The pass owns the
// context rather than taking the stop signal, so a shutdown lets the pass
// in flight finish its writes, and the loop returns on the next turn. A
// pass whose API server stops answering ends here, and the next pass
// starts clean. It is a variable so a test drives a short timeout.
var passTimeout = 30 * time.Second

// Operator holds what every pass needs: the client it reads and writes
// through, the settings it stamps into each pod and Job it
// creates, the bus, and the desks the bus folds each message onto. They
// are fields rather than globals so a test builds an operator around a
// desk and a cluster it controls.
type operator struct {
	client         *Client
	scannerImage   string
	corrosionImage string
	browserImage   string
	busAddress     string
	topicBase      string
	bus            *Bus
	reports        *reports

	// The provider endpoint every reachability check calls and the client it
	// calls through, as fields so a test points them at a server of its own and
	// no test reaches the internet.
	providerBase   string
	providerClient *http.Client

	// The namespace this operator runs in, which is what the
	// webhook address it reports on every Library names, and the address
	// its own webhook server listens on.
	namespace      string
	webhookAddress string

	// Whether each namespace's reporter is on the bus, which is
	// what "online" means for every Library of that namespace.
	reporters *reporters

	// The webhook paths the server holds for the next pass, one
	// set per Library. A path becomes a scan Job on the pass that finds
	// no full walk running.
	paths *heldPaths

	// The play requests the bus handler holds for the next pass. A
	// screen's choice reaches the API server only here, because the
	// screen pod holds no credential of its own.
	plays *playRequests

	// Wake is the loop's own wake channel, and one channel serves the
	// two watches and the bus handler, because a wake says nothing
	// beyond "read the collection again".
	wake chan struct{}

	// The recreate backoff of each departing Library's cleanup Job,
	// keyed the way the report desk keys a Library, and dropped when
	// the Library goes.
	cleanupStands map[string]cleanupStand
}

// NewOperator builds the operator and the two things it listens
// through: the desk that holds each Library's newest report, and the
// bus subscriptions that fill it. The subscriptions are remembered
// here and sent on every connection, so they outlive a broker
// restart.
func newOperator(client *Client, scannerImage, corrosionImage, browserImage, busAddress, topicBase, namespace, webhookAddress string) *operator {
	wake := make(chan struct{}, 1)
	library := &operator{
		client:         client,
		scannerImage:   scannerImage,
		corrosionImage: corrosionImage,
		browserImage:   browserImage,
		busAddress:     busAddress,
		topicBase:      topicBase,
		namespace:      namespace,
		webhookAddress: webhookAddress,
		reports:        newReports(wake),
		reporters:      newReporters(wake),
		paths:          newHeldPaths(wake),
		plays:          newPlayRequests(wake),
		wake:           wake,
		cleanupStands:  map[string]cleanupStand{},
		providerBase:   defaultProviderBase,
		providerClient: &http.Client{Timeout: providerCheckTimeout},
	}
	// The operator names no will. Its one publish is the empty
	// retained payload that drops a departed library's topics, and a
	// will replaces nothing about that: there is no message of the
	// operator's own that a broker should stand in for when the
	// connection breaks.
	library.bus = newBus(busAddress, "library-operator", nil, nil, library.handleBusMessage)
	library.bus.Subscribe(libraryStatusFilter(topicBase))
	library.bus.Subscribe(catalogAvailabilityFilter(topicBase))
	library.bus.Subscribe(playRequestFilter(topicBase))
	return library
}

// Operate reads the operator's environment and returns its failure
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
	// The media browser one screen pod runs. The operator refuses to
	// start without it for the reason it refuses without the other two.
	browserImage := os.Getenv(browserImageVariable)
	if browserImage == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the media browser image", browserImageVariable)
	}
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the broker", busAddressVariable)
	}
	// The namespace the operator's own Service is in, which is
	// what the address it reports on every Library names. The Deployment
	// reads it off the pod with the downward API.
	namespace := os.Getenv(operatorNamespaceVariable)
	if namespace == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the operator's namespace", operatorNamespaceVariable)
	}
	// The topic base has a default, because a cluster that runs one
	// bus needs no policy for it.
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}
	// The port the webhook endpoint answers on, with a default,
	// because a cluster that takes the manifest as it ships needs no
	// policy for it.
	port := os.Getenv(webhookPortVariable)
	if port == "" {
		port = defaultWebhookPort
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

	return newOperator(client, scannerImage, corrosionImage, browserImage,
		busAddress, topicBase, namespace, ":"+port).run(stopped, os.Stdout)
}

// Run is the operator without the process around it, so a test drives
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
	pods, err := ListCatalogMemberPods(startup, o.client)
	if err != nil {
		return fmt.Errorf("listing catalog member pods: %w", err)
	}
	// A Player belongs to media-operator, and a cluster that runs none
	// serves no such collection. That failure is reported and the operator
	// carries on with the libraries, so a cluster with no screens still scans.
	// The watch then resumes from an empty version, which the API server reads
	// as the state it holds now.
	players, err := ListPlayers(startup, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing players: %v\n", err)
		players = &PlayerList{}
	}
	// The providers are read on the same terms as the Players: a cluster that
	// has not applied the CRD serves no such collection, and its libraries are
	// still scanned and still reported.
	providers, err := ListMetadataProviders(startup, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing metadata providers: %v\n", err)
		providers = &MetadataProviderList{}
	}
	fmt.Fprintf(report, "library.liken.sh: operating %d libraries over %s\n",
		len(libraries.Items), o.busAddress)

	go watchLibraries(o.client, libraries.Metadata.ResourceVersion, o.wake)
	go watchCatalogs(o.client, catalogs.Metadata.ResourceVersion, o.wake)
	go watchPods(o.client, pods.Metadata.ResourceVersion, o.wake)
	go watchPlayers(o.client, players.Metadata.ResourceVersion, o.wake)
	go watchMetadataProviders(o.client, providers.Metadata.ResourceVersion, o.wake)

	// The webhook endpoint runs for the life of the operator. A
	// failure to listen ends the loop, because an operator that reports
	// an address nothing answers is worse than one that stops.
	serving := make(chan error, 1)
	go func() { serving <- o.serveWebhooks(stopped, o.webhookAddress) }()

	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		o.pass()
		select {
		case <-stopped.Done():
			return nil
		case err := <-serving:
			if err != nil {
				return fmt.Errorf("serving webhooks on %s: %w", o.webhookAddress, err)
			}
			return nil
		case <-o.wake:
		case <-ticker.C:
		}
	}
}

// Pass reconciles every Library in the cluster against its namespace's
// Catalog, then stands the catalog cluster of each namespace that holds
// a Catalog. That is the namespace's work and not one Library's. A
// failure on one object is reported and the pass continues, because one
// library's broken claim must not freeze every other library's status.
//
// it stands the screen of every delegated Player as well, after the
// libraries and before the catalog cluster, so the catalog step reads the
// screen pods this pass created. A pod with no address yet reaches the
// EndpointSlice on the pass after the kubelet gives it one.
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
	// The Players are read after the Catalogs and reported the same
	// way, except that a failure here is not the end of the pass: a cluster
	// with no media-operator serves no Players, and its libraries are still
	// scanned and still reported.
	players, err := ListPlayers(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing players: %v\n", err)
		players = &PlayerList{}
	}
	// The Jobs and the member pods are read once for the whole
	// pass, because a Library's status reads both and the catalog step
	// reads the pods again. A list that fails ends the pass: without the
	// Jobs the pass cannot tell what is running, and without the pods it
	// cannot tell whether a namespace's catalog stands.
	jobs, err := ListWorkerJobs(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing worker jobs: %v\n", err)
		return
	}
	members, err := ListCatalogMemberPods(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing catalog member pods: %v\n", err)
		return
	}
	// The providers of every namespace, read and checked once per pass. A
	// cluster that has not applied the CRD serves no such collection, and its
	// libraries are still scanned and still reported.
	providers, err := ListMetadataProviders(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing metadata providers: %v\n", err)
		providers = &MetadataProviderList{}
	}
	byNamespace := catalogsByNamespace(catalogs.Items)
	now := time.Now().UTC()
	// The succeeded Jobs of every namespace go first, so what a person sees in
	// kubectl get pods is what runs now and what failed.
	o.retireSucceededJobs(ctx, jobs.Items, now)
	checked := o.checkProviders(ctx, providers.Items, now)

	live := make(map[string]bool, len(libraries.Items))
	for index := range libraries.Items {
		library := &libraries.Items[index]
		namespace, name := library.Metadata.Namespace, library.Metadata.Name
		live[libraryKey(namespace, name)] = true

		choice := singleCatalog(byNamespace[namespace])
		choice.pod = catalogPodOf(choice.catalog, members.Items)

		// A deleting Library takes the departure and never the
		// reconcile, because the reconcile would stand the schedule
		// back up to rewrite the rows the sweep is deleting.
		if library.Metadata.deleting() {
			if err := o.depart(ctx, library, choice, jobs.Items); err != nil {
				fmt.Fprintf(os.Stderr, "departing library %s/%s: %v\n", namespace, name, err)
			}
			continue
		}

		if err := o.reconcile(ctx, library, choice, jobs.Items, checked, now); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling library %s/%s: %v\n", namespace, name, err)
		}
	}
	// The collection this pass read is the whole set of Libraries, so
	// anything else the desk holds belongs to a Library that is gone.
	// The pass clears the topics of every key it drops, because the
	// desk holds a key only while a retained message stands on the
	// bus: a report from before this operator cleared topics is
	// standing there still. Only a pass may clear one, because
	// the bus handler holds no Library list; the subscription
	// delivers the litter, the desk holds it, and the next pass
	// drops it.
	for _, key := range o.reports.retain(live) {
		namespace, name, _ := strings.Cut(key, "/")
		o.clearLibraryTopics(namespace, name)
	}
	o.paths.retain(live)
	for key := range o.cleanupStands {
		if !live[key] {
			delete(o.cleanupStands, key)
		}
	}

	// The screen pods are read once for every namespace, so the pass
	// deletes only a pod that stands. A list that fails costs the pass
	// its deletes and nothing else: a delegated Player is still stood,
	// and the pod of one that switched away goes on the next pass.
	screens, err := ListScreenPods(ctx, o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing screen pods: %v\n", err)
		screens = &PodList{}
	}
	for _, namespace := range screenNamespaces(players.Items) {
		// A screen's catalog claim is sized and classed by the
		// namespace's one Catalog, and a namespace with none, or with more
		// than one, stands its screens on an emptyDir.
		o.reconcileScreens(ctx, namespace, singleCatalog(byNamespace[namespace]).catalog,
			players.Items, libraries.Items, screens.Items, now)
	}
	// The play requests are served last, on the collections this pass
	// already read. A request is one moment: the pass creates its Play
	// now or drops it, and the person presses again.
	o.createPlays(ctx, players.Items, libraries.Items)

	o.reconcileCatalogs(ctx, byNamespace, members.Items, now)
}

// HandleBusMessage folds one message from the broker onto the place
// that holds it: a library report onto the desk, a play request onto
// its queue. It runs on the bus reader's goroutine, so it does nothing
// beyond the fold, and the wake each fold raises is what carries the
// message into the next pass.
func (o *operator) handleBusMessage(topic string, payload []byte) {
	// A play request is the one message that is not a report. It is
	// held for the next pass, because creating a Play is a write and
	// the bus reader's goroutine makes none.
	if namespace, player, ok := parsePlayRequestTopic(o.topicBase, topic); ok {
		o.readPlayRequest(namespace, player, topic, payload)
		return
	}
	// A namespace's reporter says online or offline on a topic of
	// its own, and that one signal stands for every Library of the
	// namespace.
	if namespace, ok := parseCatalogAvailabilityTopic(o.topicBase, topic); ok {
		if len(payload) != 0 {
			o.reporters.mark(namespace, string(payload) == availabilityOnline)
		}
		return
	}
	namespace, name, kind, ok := parseLibraryTopic(o.topicBase, topic)
	if !ok {
		return
	}
	// An empty payload is how a retained topic is cleared, so it
	// carries nothing to fold. The operator subscribes to the topics it
	// clears, so its own clears come back to it, and folding one would
	// put back the desk state the pass just dropped.
	if len(payload) == 0 || kind != libraryStatusKind {
		return
	}
	var report libraryReport
	if err := json.Unmarshal(payload, &report); err != nil {
		fmt.Fprintf(os.Stderr, "reading the report on %s: %v\n", topic, err)
		return
	}
	o.reports.fold(namespace, name, report)
}
