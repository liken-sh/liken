//go:build !pod

package main

// The operator process: its environment, the Lease it holds, the
// watches it reads the cluster through, and the loop of passes. This
// file is not in the pod build (operate_pod.go says why).

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// busStopTimeout bounds the wait for the bus session to end on a
// shutdown. A session that does not end in time keeps the Lease, so
// the next leader's session does not meet it.
const busStopTimeout = 5 * time.Second

// processSettings are what the Deployment tells the operator through
// its environment.
type processSettings struct {
	busAddress     string
	namespace      string
	pod            string
	topicBase      string
	mediaTopicBase string
	port           string
	metricsAddress string
}

// Operate reads the operator's environment and returns its failure
// instead of exiting, so main is the only place that ends the process.
// A missing setting fails here, before the first pass, because a pod
// that cannot name the images it creates has nothing to reconcile with.
// The one exit that is not main's is a lost Lease (the
// kubernetes/election package says why).
func operate() error {
	settings, err := readSettings()
	if err != nil {
		return err
	}

	// The kubelet stops the pod with SIGTERM, and a person who runs
	// the binary by hand stops it with SIGINT. Both end the context,
	// and the process exits with a zero status. The stop signal is
	// registered before the first request, so a SIGTERM during start-up
	// reaches the handler.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Each request ends within the election's request timeout, so a
	// write that the guard allowed just before the leader stopped
	// renewing ends before another copy can take the Lease.
	client, err := inClusterClient(election.RequestTimeout)
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	library, leader, watching, err := startProcess(settings, client, inClusterConfig(), os.Exit, os.Stdout)
	if err != nil {
		return err
	}
	return library.run(stopped, os.Stdout, watching, leader)
}

// readSettings reads the settings from the environment, and names the
// first one that is missing.
func readSettings() (processSettings, error) {
	s := processSettings{busAddress: os.Getenv(busAddressVariable)}
	if s.busAddress == "" {
		return s, fmt.Errorf("%s is unset; the Deployment must name the broker", busAddressVariable)
	}
	// The namespace the operator's own Service is in, which is
	// what the address it reports on every Library names, and where its
	// Lease is. The Deployment reads it off the pod with the downward API.
	s.namespace = os.Getenv(operatorNamespaceVariable)
	if s.namespace == "" {
		return s, fmt.Errorf("%s is unset; the Deployment must name the operator's namespace", operatorNamespaceVariable)
	}
	// The pod's name is this process's part in the Lease.
	s.pod = os.Getenv(podNameVariable)
	if s.pod == "" {
		return s, fmt.Errorf("%s is unset; the Deployment must name the operator's pod", podNameVariable)
	}
	// The topic base has a default, because a cluster that runs one
	// bus needs no policy for it.
	s.topicBase = os.Getenv(topicBaseVariable)
	if s.topicBase == "" {
		s.topicBase = defaultTopicBase
	}
	s.mediaTopicBase = mediaTopicBaseOf(os.Getenv(mediaTopicBaseVariable))
	// The port the webhook endpoint answers on, with a default,
	// because a cluster that takes the manifest as it ships needs no
	// policy for it.
	s.port = os.Getenv(webhookPortVariable)
	if s.port == "" {
		s.port = defaultWebhookPort
	}
	// The metrics listener's address, with no default: milestone 65
	// says an empty address serves no metrics, so a cluster that wants
	// none only has to leave the variable unset.
	s.metricsAddress = os.Getenv(metricsAddressVariable)
	return s, nil
}

// startProcess builds the operator from its settings and its two
// connections to the API server, and starts the election. client sends
// every write and every read the watches do not answer, and config
// serves the watches and the Lease. A test calls it with connections to
// a fake API server. exit ends the process after a lost Lease, and log
// receives the election's lines.
func startProcess(s processSettings, client *apiclient.Client, config *rest.Config,
	exit func(int), log io.Writer) (*operator, *leadership, dynamic.Interface, error) {
	watching, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the client for the watches: %w", err)
	}
	leader, err := newLeadership(config, s.namespace, s.pod, exit,
		func(line string) { fmt.Fprintln(log, line) })
	if err != nil {
		return nil, nil, nil, fmt.Errorf("leader election: %w", err)
	}
	// Every write the operator sends, from a pass and from the Event
	// recorder, asks the election first, so a copy that cannot show a
	// current renewal of the Lease stops writing before another copy can
	// take it. The guard refuses no read, so the read of the operator's
	// own pod below runs before the lead.
	client = client.WithWriteGuard(leader.MayWrite)

	// The read of the operator's own pod has the same timeout as a pass.
	naming, endNaming := context.WithTimeout(context.Background(), passTimeout)
	defer endNaming()
	stamped, err := operatorImages(naming, client, s.namespace)
	if err != nil {
		return nil, nil, nil, err
	}

	library := newOperator(client, stamped, s.busAddress, s.topicBase, s.namespace, ":"+s.port)
	library.mediaTopicBase = s.mediaTopicBase
	// The recorder outlives the stop signal, so the Events of the last
	// pass are written, and stepDown shuts it down before the release.
	// The instance is the pod's name, which os.Hostname answers in the
	// pod.
	library.recorder = events.New(context.Background(), client, eventComponent, events.Options{})
	library.metricsAddress = s.metricsAddress
	if library.metricsAddress != "" {
		library.metrics = newMetrics(stamped.version)
	}
	leader.Run()
	return library, leader, watching, nil
}

// inClusterConfig is the configuration of the watches and the Lease,
// from the same address and ServiceAccount files the shared client
// reads. The token is a file and not a value, so client-go reads
// it again when the kubelet rotates it. Both clients send JSON: the
// dynamic client decodes nothing else, and the Lease is one small
// object.
func inClusterConfig() *rest.Config {
	return &rest.Config{
		Host:            inClusterBase(),
		BearerTokenFile: serviceAccountDir + "/token",
		TLSClientConfig: rest.TLSClientConfig{CAFile: serviceAccountDir + "/ca.crt"},
		ContentConfig:   rest.ContentConfig{ContentType: "application/json"},
	}
}

// lease is the election run takes part in. await blocks until this
// process holds the Lease, and answers false when the stop signal comes
// first. stepDown releases the Lease after the last pass, once quiet
// has stopped everything else that acts and the recorder has written
// its last Event.
type lease interface {
	await(stop context.Context) bool
	stepDown(quiet func() bool, recorder *events.Recorder)
}

// Run is the operator without the process around it, so a test drives
// the whole loop against an API server it controls. It returns when
// the context ends, which is the stop signal.
//
// The metrics listener and the webhook server start before the Lease
// is held, in every copy. A rollout runs the new pod beside the old one,
// the Service sends a webhook to either, and the waiting copy holds each
// path it receives for its first pass. Everything else waits for the
// Lease: the bus session, because two copies under one client id would
// end each other's sessions, the watches, and the passes.
func (o *operator) run(stopped context.Context, report io.Writer, watching dynamic.Interface, held lease) error {
	// The metrics listener runs for the life of the operator, on no
	// address where the cluster names none. Unlike the webhook server, a
	// failure here never ends the loop: milestone 65 says a failure in
	// the listener must never block the work the process exists for, so
	// this operator logs it and keeps scanning libraries nothing can see.
	if o.metricsAddress != "" {
		go func() {
			if err := o.metrics.serve(stopped, o.metricsAddress); err != nil {
				fmt.Fprintf(os.Stderr, "serving metrics on %s: %v\n", o.metricsAddress, err)
			}
		}()
	}

	// The webhook endpoint runs for the life of the operator. A
	// failure to listen ends the loop, because an operator that reports
	// an address nothing answers is worse than one that stops.
	serving := make(chan error, 1)
	go func() { serving <- o.serveWebhooks(stopped, o.webhookAddress) }()

	if !held.await(stopped) {
		return nil
	}

	// The bus session ends before the Lease is released, and Run returns
	// only after its reader, which runs the handler, has returned.
	busContext, stopBus := context.WithCancel(context.Background())
	busDone := make(chan struct{})
	go func() {
		o.bus.Run(busContext)
		close(busDone)
	}()
	// The watches stop before the Lease is released too. They write
	// nothing, and a copy that no longer leads reads nothing from them.
	watchContext, stopWatches := context.WithCancel(context.Background())
	watched := startWatches(watchContext, watching, o.wake, o.metrics, o.client, o.versions)
	defer held.stepDown(func() bool {
		stopWatches()
		watched.wait()
		stopBus()
		select {
		case <-busDone:
			return true
		case <-time.After(busStopTimeout):
			return false
		}
	}, o.recorder)

	// The first read of every collection has the same bound as a pass.
	// The pass acts on what the watches hold, so it starts only once the
	// collections it needs have been read.
	startup, endStartup := context.WithTimeout(context.Background(), passTimeout)
	defer endStartup()
	if err := watched.settle(startup); err != nil {
		return err
	}
	o.watched = watched
	libraries, err := watched.readLibraries(startup)
	if err != nil {
		return fmt.Errorf("reading the libraries: %w", err)
	}
	fmt.Fprintf(report, "library.liken.sh: operating %d libraries over %s\n",
		len(libraries.Items), o.busAddress)

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
