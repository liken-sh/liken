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

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// busStopTimeout bounds the wait for the bus session to end on a
// shutdown. A session that does not end in time keeps the Lease, so
// the next leader's session does not meet it.
const busStopTimeout = 5 * time.Second

// Operate reads the operator's environment and returns its failure
// instead of exiting, so main is the only place that ends the process
// and a test drives the whole setup. A missing setting fails here,
// before the first pass, because a pod that cannot name the images it
// creates has nothing to reconcile with. The one exit that is not
// main's is a lost Lease (leader.go says why).
func operate() error {
	busAddress := os.Getenv(busAddressVariable)
	if busAddress == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the broker", busAddressVariable)
	}
	// The namespace the operator's own Service is in, which is
	// what the address it reports on every Library names, and where its
	// Lease is. The Deployment reads it off the pod with the downward API.
	namespace := os.Getenv(operatorNamespaceVariable)
	if namespace == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the operator's namespace", operatorNamespaceVariable)
	}
	// The pod's name is this process's part in the Lease.
	pod := os.Getenv(podNameVariable)
	if pod == "" {
		return fmt.Errorf("%s is unset; the Deployment must name the operator's pod", podNameVariable)
	}
	// The topic base has a default, because a cluster that runs one
	// bus needs no policy for it.
	topicBase := os.Getenv(topicBaseVariable)
	if topicBase == "" {
		topicBase = defaultTopicBase
	}
	mediaTopicBase := mediaTopicBaseOf(os.Getenv(mediaTopicBaseVariable))
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
	config := inClusterConfig(client)
	watching, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("the client for the watches: %w", err)
	}
	leader, err := newLeadership(config, namespace, pod, operatorLeaseTiming, os.Exit,
		func(line string) { fmt.Println(line) })
	if err != nil {
		return fmt.Errorf("leader election: %w", err)
	}

	// The kubelet stops the pod with SIGTERM, and a person who runs
	// the binary by hand stops it with SIGINT. Both end the context,
	// and the process exits with a zero status.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// The stop signal is registered before the pod read, so a SIGTERM
	// during start-up reaches the handler, and the read has the same
	// timeout as a pass.
	naming, endNaming := context.WithTimeout(context.Background(), passTimeout)
	defer endNaming()
	stamped, err := operatorImages(naming, client, namespace)
	if err != nil {
		return err
	}

	library := newOperator(client, stamped.scanner, stamped.corrosion, stamped.browser,
		stamped.ffmpeg, busAddress, topicBase, namespace, ":"+port)
	library.mediaTopicBase = mediaTopicBase

	// The metrics listener's address, with no default: milestone 65
	// says an empty address serves no metrics, so a cluster that wants
	// none only has to leave the variable unset.
	library.metricsAddress = os.Getenv(metricsAddressVariable)
	if library.metricsAddress != "" {
		library.metrics = newMetrics(stamped.version)
	}
	leader.run()
	return library.run(stopped, os.Stdout, watching, leader)
}

// inClusterConfig is the configuration of the watches and the Lease,
// from the same address and ServiceAccount files the operator's own
// Client reads. The token is a file and not a value, so client-go reads
// it again when the kubelet rotates it. Both clients send JSON: the
// dynamic client decodes nothing else, and the Lease is one small
// object.
func inClusterConfig(client *Client) *rest.Config {
	return &rest.Config{
		Host:            client.base,
		BearerTokenFile: client.credentials + "/token",
		TLSClientConfig: rest.TLSClientConfig{CAFile: client.credentials + "/ca.crt"},
		ContentConfig:   rest.ContentConfig{ContentType: "application/json"},
	}
}

// lease is the election run takes part in. await blocks until this
// process holds the Lease, and answers false when the stop signal comes
// first. stepDown releases the Lease after the last pass, once quiet
// has stopped everything else that acts.
type lease interface {
	await(stop context.Context) bool
	stepDown(quiet func() bool)
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
	watched := startWatches(watchContext, watching, o.wake, o.metrics)
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
	})

	// The first read of every collection has the same bound as a pass.
	// The pass acts on what the watches hold, so it starts only once the
	// collections it needs have been read.
	startup, endStartup := context.WithTimeout(context.Background(), passTimeout)
	defer endStartup()
	if err := watched.settle(startup); err != nil {
		return err
	}
	o.watched = watched
	libraries, err := watched.readLibraries()
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
