// liken-cluster-operator is the program that watches the fleet.
//
// Each machine's operator reports on itself. This leaves verdicts
// that no single machine can write: a dead machine cannot report
// that it is dead, no machine can total a headcount that includes
// itself without conflicting with the other machines' operators, and
// someone who can see every machine's request must hand out reboot
// turns. This program writes exactly those verdicts: the Lost phase
// on silent machines, the Cluster's status, and the rollout's
// grants. It also refreshes the OS's own DaemonSet pods after
// upgrades (see steward.go).
//
// The machine operator is privileged and node-local: a DaemonSet
// with hostPath mounts, running on the machine it manages. This
// program is different. It is an ordinary workload: a single-replica
// Deployment with no mounts, no host network, and no privilege. The
// Kubernetes API is its only input and its only output. This is what
// lets its RBAC role match exactly what a fleet observer needs: read
// machines and heartbeats, write statuses, evict stale OS pods.
//
// Only one copy of this program acts at a time. The Deployment rolls a
// new pod in beside the old one, and each copy competes for a leader
// election Lease; only the copy that holds it watches the fleet and
// writes. leader.go explains the election and its limits.
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

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// component is this program's name in every metric that carries one.
const component = "liken-cluster-operator"

// metricsAddress is where this operator answers a Prometheus scrape.
// The default is the port that plans/completed/65-prometheus-metrics.md gives
// every process on the cluster network, so the binary carries the
// contract and the pod template only has to name the port it
// exposes. An empty value turns the listener off, for an owner who
// runs no Prometheus.
var metricsAddress = flag.String("metrics-address", ":9200",
	"the address to serve /metrics on; empty serves no metrics")

func main() {
	flag.Parse()
	fmt.Println(component, machine.Version)

	// A SIGTERM from the kubelet ends the loop after the sweep in
	// flight, and the process then releases the Lease, so the copy that
	// waits beside it takes over at once.
	stop, _ := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)

	// The metrics registry outlives every pass, because a counter's
	// whole value is that it accumulates (metrics.go). A copy that
	// waits for the Lease serves its runtime metrics too.
	operatorMetrics, clusterLayer := serveMetrics(*metricsAddress)

	// A failure during setup ends the process deliberately. This is
	// the same crash-only method the machine operator uses: kubelet
	// restarts the pod with backoff, and the failure shows in
	// `kubectl get pods`.
	client, err := kubernetes.InClusterClient("")
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("in-cluster config for the watches: %v", err)
	}

	// Nothing below runs until this copy holds the Lease, and every
	// write asks the election first (mayWrite in leader.go), again
	// before each send after a 429. A SIGTERM ends the wait after a
	// 429, so a throttled sweep does not hold back the release of the
	// Lease, and a write already sent still runs to its answer.
	leader := lead(stop, newUnelectedGauge(operatorMetrics))
	client = client.WithWriteGuard(leader.mayWrite).WithWaitContext(stop)

	// The watches start with the lead, so a copy that has never acted
	// holds no copy of the fleet and opens no stream (watches.go names each watch and
	// what wakes the loop). The wake channel has one slot, so a burst
	// of Machine changes makes one wake, and one sweep over the newest
	// state answers the whole burst.
	wakes := make(chan struct{}, 1)
	fleet := watchFleet(context.Background(), watcher, client, watch.Signal(wakes),
		operatorMetrics.WatchRestarted)

	// The ticker is a clock. A heartbeat ages past the staleness limit
	// with no event, because an aging Lease is not a write. A granted
	// reboot turn passes rolloutStallAfter the same way, and the
	// channel poller and the engine probe keep their own intervals.
	// Ten seconds keeps those verdicts inside the same window that the
	// machine operators work on. The objects a sweep judges come from
	// the watches' copies, so a tick costs the API server one read: the
	// flux deploy key Secret while the feature is declared, or the
	// flux-system Namespace while it is not (watches.go says why those
	// two stay direct).
	ticker := time.NewTicker(10 * time.Second)
	operate(stop, leader.mayAct, fleet, wakes, ticker.C, operatorMetrics, clusterLayer)
	leader.stepDown()
}

// operate finds the Cluster and sweeps the fleet until stop ends. A
// wake or a tick starts the next sweep, and stop ends the loop only
// between two sweeps, so the sweep in flight finishes its writes.
// mayAct blocks before each sweep until this copy may act, and answers
// false when stop ended first.
func operate(stop context.Context, mayAct func(context.Context) bool, fleet *fleetReader,
	wakes <-chan struct{}, ticks <-chan time.Time, om *metrics.Operator, cm *clusterMetrics) {
	// This program takes no configuration at all. It finds the
	// Cluster it operates in the Clusters' copy, because a fleet has
	// exactly one Cluster, and the machine operators seed it from the
	// image. Until the CRD is served and some machine's seed lands, in
	// the first minutes of a brand-new cluster, there is nothing to
	// operate, so this program waits.
	clusterDoc := awaitCluster(fleet, wakes, stop)
	if clusterDoc == nil {
		return
	}
	name := clusterDoc.Metadata.Name
	fmt.Printf("operating cluster %s\n", name)

	// Two values outlive a pass, the same way the machine operator's
	// release fetcher does. The sweep stays level-triggered and
	// stateless, and each of these remembers only what it needs to
	// avoid asking too often. The channel poller keeps when it last
	// asked and the channel's last answer. The flux engine probe keeps
	// when it last asked whether the engine is still there.
	poller := newChannelPoller()
	probe := &engineProbe{}

	for mayAct(stop) {
		started := time.Now()
		err := sweep(fleet, name, poller, probe, cm)
		om.ObserveReconcile(clusterKind, time.Since(started), err)
		select {
		case <-wakes:
		case <-ticks:
		case <-stop.Done():
		}
	}
}

// sweep runs one pass of the cluster operator's whole job, always
// starting from the cluster's current state. It reads the Cluster
// fresh, because its spec drives the rollout. It gives the channel
// poller its look at the spec. Then it lets the fleet sweep list the
// fleet, judge it, and write the result, carrying the engine probe
// along so the flux engine's care keeps its own cadence.
func sweep(reads *fleetReader, name string, poller *channelPoller, probe *engineProbe, cm *clusterMetrics) error {
	clusterDoc, err := reads.cluster(name)
	if err != nil {
		fmt.Printf("reading cluster %s: %v\n", name, err)
		return err
	}
	poller.Observe(clusterDoc.Spec.Releases,
		clusterDoc.Metadata.Annotations[cluster.CheckReleasesAnnotation], time.Now())
	return sweepFleet(reads, clusterDoc, poller.Available(), probe, cm, time.Now())
}

// awaitCluster waits until a Cluster exists, and answers it. A 404
// response only means the CRD is not served yet. An empty list means
// no machine has seeded the object yet. Both conditions resolve
// themselves as the fleet boots. The Clusters' copy wakes the loop
// when the first Cluster arrives. While the copy cannot answer, each
// look reads the API server, and the ticker's pace bounds those reads.
// It answers nil when stop ends first.
func awaitCluster(reads *fleetReader, wakes <-chan struct{}, stop context.Context) *cluster.Cluster {
	retry := time.NewTicker(10 * time.Second)
	defer retry.Stop()
	for {
		clusters, err := reads.clusters()
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Printf("listing clusters: %v\n", err)
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
