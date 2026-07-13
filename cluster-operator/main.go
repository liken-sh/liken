// liken-cluster-operator: the program that watches the fleet.
//
// Every machine's operator reports on itself, which leaves the
// verdicts no single machine can write: a dead machine can't report
// that it's dead, no machine can total a headcount it's part of
// without stepping on the other machines' operators, and reboot
// turns have to be handed out by someone who can see everyone
// asking. This program
// writes exactly those verdicts: the Lost phase on silent machines,
// the Cluster's status, and the rollout conductor's grants. It also
// stewards the OS's own DaemonSet pods after upgrades (steward.go).
//
// Where the machine operator is privileged and node-local (a
// DaemonSet with hostPath mounts, standing on the machine it
// manages), this one is an ordinary workload: a single-replica
// Deployment with no mounts, no host network, and no privilege. The
// Kubernetes API is its only input and its only output, which is
// what lets its RBAC be exactly a fleet observer's: read machines
// and heartbeats, write statuses, evict stale OS pods.
//
// There is deliberately no leader election here. kube-controller-
// manager and kube-scheduler elect leaders because they run beneath
// the scheduler: static pods on every control-plane node, with hot
// standbys and a lease as their only way to agree on one active
// copy. This program runs above the scheduler, and "run exactly one
// somewhere" is the scheduler's own job: a single-replica Deployment
// with strategy Recreate. If the machine under it dies, Kubernetes
// reschedules the pod (the manifest's tolerations keep that window
// short), and the new pod resumes from absolute state, because
// nothing here keeps state anywhere else. Even a brief overlap of
// two copies would only produce agreement: both compute the same
// verdicts from the same data, the writers are change-only, and
// optimistic concurrency already serializes the writes.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

func main() {
	fmt.Println("liken-cluster-operator", machine.Version)

	// Failures during setup end the process deliberately, the same
	// crash-only style as the machine operator: kubelet restarts the
	// pod with backoff, and the failure shows in `kubectl get pods`.
	client, err := kubernetes.InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

	// This program takes no configuration at all: it finds the
	// Cluster it operates by listing, because a fleet has exactly one
	// and the machine operators seed it from the image. Until the CRD
	// is served and some machine's seed lands (the first minutes of a
	// brand-new cluster), there is nothing to operate, so wait.
	cluster := awaitCluster(client)
	name := cluster.Metadata.Name
	fmt.Printf("operating cluster %s\n", name)

	// The same level-triggered loop as the machine operator, pointed
	// at the whole fleet: the watch spans every Machine with no
	// fieldSelector, because any machine's transition can change the
	// Cluster's phase, a rollout's budget, or a Lost verdict. Events
	// are wake signals and nothing more. The pass below re-reads
	// everything it judges, so the drained objects themselves are
	// discarded, and missing one event can never matter. The empty
	// resourceVersion starts the watch at the server's current state;
	// the first recovery list establishes a precise resume point.
	events := make(chan *machine.Machine, 32)
	go kubernetes.WatchMachines(client, "", "", events)

	// The channel poller is the one piece of state that outlives a
	// pass, exactly like the machine operator's release fetcher: the
	// sweep stays level-triggered and stateless, and the poller
	// remembers only what laziness requires (when it last asked, and
	// the channel's last answer).
	poller := newChannelPoller()

	// The ticker is the backstop for the changes no Machine event
	// announces: heartbeats aging past staleness (a Lease renewal is
	// not a Machine write) and Cluster spec edits like a new version
	// target. Ten seconds keeps those verdicts inside the same window
	// the machine operators work on.
	ticker := time.NewTicker(10 * time.Second)
	for {
		sweep(client, name, poller)
		select {
		case <-events:
			drainEvents(events)
		case <-ticker.C:
		}
	}
}

// sweep is one pass of the cluster operator's whole job, always from
// absolute state: read the Cluster fresh (its spec drives the
// rollout), give the channel poller its look at the spec, then let
// the fleet sweep list, judge, and write.
func sweep(c *kubernetes.Client, name string, poller *channelPoller) {
	cluster, err := kubernetes.GetCluster(c, name)
	if err != nil {
		fmt.Printf("reading cluster %s: %v\n", name, err)
		return
	}
	poller.Observe(cluster.Spec.Releases, time.Now())
	sweepFleet(c, cluster, poller.Available(), time.Now())
}

// awaitCluster lists until a Cluster exists. A 404 just means the
// CRD isn't served yet; an empty list means no machine has seeded
// the object; both resolve themselves as the fleet boots.
func awaitCluster(c *kubernetes.Client) *machine.Cluster {
	for {
		clusters, err := kubernetes.ListClusters(c)
		if err != nil && !errors.Is(err, kubernetes.ErrNotFound) {
			fmt.Printf("listing clusters: %v\n", err)
		}
		if len(clusters) > 0 {
			return &clusters[0]
		}
		kubernetes.RetryPause()
	}
}

// drainEvents empties whatever the watch queued while the last pass
// ran. A busy fleet queues events faster than passes run; one pass
// over the newest state answers a whole burst, so the queued copies
// carry nothing the pass won't re-read for itself.
func drainEvents(events <-chan *machine.Machine) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
