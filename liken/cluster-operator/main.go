// liken-cluster-operator writes the parts of the fleet's state that
// need a view of every machine.
//
// Each machine operator reports only on its own machine. A machine that
// is down cannot report it, so this program sets the Lost phase on each
// machine whose heartbeat is stale. The Cluster's status totals every
// machine, and the machine operators would conflict if each wrote it. A
// reboot turn is safe to grant only from a view of every machine's
// request. So this program writes the Lost phase, the Cluster's status,
// and the rollout's reboot grants. It also evicts the stale pods of the
// OS's own DaemonSets after an upgrade (steward.go), creates the flux
// deploy key and the Flux controllers (flux.go), and deletes the
// workloads of a retracted feature (janitor.go).
//
// The program does all of this in one reconcile pass over the whole
// fleet (reconcileFleet), which a watch event or a ten-second tick
// starts. Each pass reads the current state and decides again from it.
//
// The machine operator is a privileged DaemonSet with hostPath mounts.
// This program is an ordinary single-replica Deployment with no mounts,
// no host network, and no privilege, because it reads and writes only
// through the Kubernetes API.
//
// Only one copy acts at a time. A Deployment rollout runs the new pod
// beside the old one, so the copies compete for a leader election
// Lease, and only the holder watches the fleet and writes (leader.go).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// component is this program's name in every metric that has one.
const component = "liken-cluster-operator"

// metricsAddress serves /metrics. A pod on the cluster network has its
// own port space, so every liken process there serves metrics on 9200,
// and the pod template exposes that port.
var metricsAddress = flag.String("metrics-address", ":9200",
	"the address to serve /metrics on. An empty value serves no metrics")

func main() {
	flag.Parse()
	fmt.Println(component, machine.Version)

	// On SIGTERM the loop finishes the pass in flight and the process
	// releases the Lease, so the waiting copy can take over at once.
	stop, _ := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)

	// One registry serves every pass, because a counter accumulates
	// across passes. A copy that waits for the Lease serves its runtime
	// metrics too.
	operatorMetrics, clusterLayer := serveMetrics(*metricsAddress)

	// A setup failure exits the process. The kubelet restarts the pod
	// with backoff, and the failure shows in `kubectl get pods`.
	client, err := kubernetes.InClusterClient("")
	if err != nil {
		fatal("reading the in-cluster configuration: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("reading the in-cluster configuration for the watches: %v", err)
	}

	leader := lead(stop)

	// The ticker is a clock, not a poll. Some deadlines pass with no
	// write for a watch to receive: a heartbeat goes stale, a granted
	// reboot turn passes rolloutStallAfter, and the intervals of the
	// channel poller and the Flux probe end. The tick finds each of
	// them within ten seconds. A pass reads the watch caches, so a tick
	// costs the API server one read: the flux deploy
	// key `Secret` while the flux feature is declared, or the
	// `flux-system` Namespace while it is not (watches.go gives the
	// reason those reads go to the API server).
	ticker := time.NewTicker(10 * time.Second)
	run(stop, context.Background(), leader, client, watcher, ticker.C, operatorMetrics, clusterLayer)
}

// run is the work of the copy that holds the Lease. It opens the
// watches, runs passes until stop ends, and then steps down. The watches use
// their own context, which main never cancels, so a pass that is
// running when stop ends can still read from them.
func run(stop, watches context.Context, leader *election.Lead, client *apiclient.Client,
	watcher dynamic.Interface, ticks <-chan time.Time, om *metrics.Operator, cm *clusterMetrics) {
	// Each write first checks that this copy still holds the Lease.
	client = client.WithWriteGuard(leader.MayWrite)

	// The watches open only after this copy takes the Lease, so a copy
	// that waits has no fleet in memory and no open watch (watches.go
	// lists each watch and the changes that wake the loop). The wake
	// channel has a buffer of one, so a burst of changes wakes the loop
	// once, and that pass reads the newest state.
	wakes := make(chan struct{}, 1)
	fleet := watchFleet(watches, watcher, client, watch.Signal(wakes), om.WatchRestarted)
	// The recorder posts the Events about the Machines and the Cluster
	// (events.go). It writes through the guarded client, so a copy that
	// lost the Lease posts nothing. It sends from its own goroutine, so a
	// pass never waits on an Event, and its context is not stop, so the
	// Events of the last pass are still sent.
	fleet.recorder = events.New(context.Background(), client, component, events.Options{})

	operate(stop, fleet, wakes, ticks, om, cm)
	// Shutdown returns after the recorder's last request returns, so
	// the Lease is released after the last write, and the waiting copy
	// takes it on its next try. When Shutdown times out, a request can
	// still be in flight. Then the Lease is left to expire, which gives
	// that request time to finish before another copy acts.
	flush, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := fleet.recorder.Shutdown(flush); err != nil {
		fmt.Printf("leaving the Lease to expire, because the Event recorder did not shut down: %v\n", err)
		return
	}
	leader.StepDown()
}

// operate waits for the Cluster, then reconciles the fleet until stop ends.
// A wake or a tick starts the next pass. The loop checks stop only
// between passes, so a running pass finishes its writes.
func operate(stop context.Context, fleet *fleetReader,
	wakes <-chan struct{}, ticks <-chan time.Time, om *metrics.Operator, cm *clusterMetrics) {
	// A fleet has one Cluster, which the machine operators create from
	// the boot manifest. So this program needs no flag to name it, and
	// takes the first Cluster it lists.
	clusterDoc := awaitCluster(fleet, wakes, stop)
	if clusterDoc == nil {
		return
	}
	name := clusterDoc.Metadata.Name
	fmt.Printf("operating Cluster %s\n", name)

	// A pass decides everything from the current state of the
	// cluster. These objects keep state across passes only to limit how
	// often the program repeats an outside request or a report. The
	// channel poller keeps the time of its last fetch and the version it
	// read. The Flux probe keeps the time it last checked for the
	// kustomize-controller Deployment. overduePods keeps the UIDs of the
	// overdue terminating pods that a pass has already logged.
	poller := newChannelPoller()
	probe := &fluxProbe{}
	overduePods := &overduePodReports{}

	for stop.Err() == nil {
		started := time.Now()
		err := reconcile(fleet, name, poller, probe, overduePods, cm)
		om.ObserveReconcile(clusterKind, time.Since(started), err)
		select {
		case <-wakes:
		case <-ticks:
		case <-stop.Done():
		}
	}
}

// reconcile runs one pass. It reads the Cluster, gives its release
// source and its check-releases annotation to the channel poller, and
// calls reconcileFleet with the channel's latest version.
func reconcile(reads *fleetReader, name string, poller *channelPoller, probe *fluxProbe, overduePods *overduePodReports, cm *clusterMetrics) error {
	clusterDoc, err := reads.cluster(name)
	if err != nil {
		fmt.Printf("reading Cluster %s: %v\n", name, err)
		return err
	}
	poller.Observe(clusterDoc.Spec.Releases,
		clusterDoc.Metadata.Annotations[cluster.CheckReleasesAnnotation], time.Now())
	return reconcileFleet(reads, clusterDoc, poller.Available(), probe, overduePods, cm, time.Now())
}

// awaitCluster waits until a Cluster exists and returns it, or returns
// nil when stop ends first. On a new cluster, the list returns 404
// until the API server serves the CRD, and is empty until a machine
// operator creates the Cluster. The Cluster watch wakes the loop when
// the first Cluster arrives. While the watch is not ready, each list
// goes to the API server, and the ten-second retry limits those lists.
func awaitCluster(reads *fleetReader, wakes <-chan struct{}, stop context.Context) *cluster.Cluster {
	retry := time.NewTicker(10 * time.Second)
	defer retry.Stop()
	for {
		clusters, err := reads.clusters()
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Printf("listing Clusters: %v\n", err)
		}
		if len(clusters) > 0 {
			return &clusters[0]
		}
		select {
		case <-wakes:
		case <-retry.C:
		case <-stop.Done():
			return nil
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
