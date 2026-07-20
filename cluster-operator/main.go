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
// This program deliberately has no leader election.
// kube-controller-manager and kube-scheduler elect leaders because
// they run below the scheduler: static pods on every control-plane
// node, with hot standbys and a lease as their only way to agree on
// one active copy. This program runs above the scheduler, so "run
// exactly one somewhere" is the scheduler's own job: a
// single-replica Deployment with strategy Recreate. If the machine
// under this program's pod dies, Kubernetes reschedules the pod. The
// manifest's tolerations keep that window short, and the new pod
// resumes from the cluster's current state, because this program
// keeps no state anywhere else. Even a brief overlap of two copies
// would produce agreement, not conflict: both copies compute the
// same verdicts from the same data, each writer only changes what
// needs to change, and optimistic concurrency already puts the
// writes safely in order.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

func main() {
	fmt.Println("liken-cluster-operator", machine.Version)

	// A failure during setup ends the process deliberately. This is
	// the same crash-only method the machine operator uses: kubelet
	// restarts the pod with backoff, and the failure shows in
	// `kubectl get pods`.
	client, err := kubernetes.InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

	// This program takes no configuration at all. It finds the
	// Cluster it operates by listing Cluster objects, because a
	// fleet has exactly one Cluster, and the machine operators seed
	// it from the image. Until the CRD is served and some machine's
	// seed lands, in the first minutes of a brand-new cluster, there
	// is nothing to operate, so this program waits.
	clusterDoc := awaitCluster(client)
	name := clusterDoc.Metadata.Name
	fmt.Printf("operating cluster %s\n", name)

	// This program uses the same level-triggered loop as the machine
	// operator, pointed at the whole fleet. The watch spans every
	// Machine with no fieldSelector, because any machine's transition
	// can change the Cluster's phase, a rollout's budget, or a Lost
	// verdict. Events are only wake signals. The pass below re-reads
	// everything it judges, so this program discards the drained
	// objects themselves, and a missed event can never matter. The
	// empty resourceVersion starts the watch at the server's current
	// state. The first recovery list then establishes a precise
	// resume point.
	events := make(chan *machine.Machine, 32)
	go kubernetes.WatchMachines(client, "", "", events)

	// The channel poller is the one piece of state that outlives a
	// pass, the same way the machine operator's release fetcher does.
	// The sweep stays level-triggered and stateless, and the poller
	// remembers only what it needs to avoid asking too often: when it
	// last asked, and the channel's last answer.
	poller := newChannelPoller()

	// The ticker catches the changes that no Machine event announces:
	// a heartbeat aging past the staleness limit (a Lease renewal is
	// not a Machine write), and a Cluster spec edit such as a new
	// version target. Ten seconds keeps those verdicts inside the
	// same window that the machine operators work on.
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

// sweep runs one pass of the cluster operator's whole job, always
// starting from the cluster's current state. It reads the Cluster
// fresh, because its spec drives the rollout. It gives the channel
// poller its look at the spec. Then it lets the fleet sweep list the
// fleet, judge it, and write the result.
func sweep(c *kubernetes.Client, name string, poller *channelPoller) {
	clusterDoc, err := kubernetes.GetCluster(c, name)
	if err != nil {
		fmt.Printf("reading cluster %s: %v\n", name, err)
		return
	}
	poller.Observe(clusterDoc.Spec.Releases, time.Now())
	sweepFleet(c, clusterDoc, poller.Available(), time.Now())
}

// awaitCluster lists Cluster objects until one exists. A 404
// response only means the CRD is not served yet. An empty list means
// no machine has seeded the object yet. Both conditions resolve
// themselves as the fleet boots.
func awaitCluster(c *kubernetes.Client) *cluster.Cluster {
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
// ran. A busy fleet queues events faster than passes run. One pass
// over the newest state answers a whole burst of events, so the
// queued copies carry nothing that the pass will not re-read for
// itself.
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
