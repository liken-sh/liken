// liken-machine-operator: the program that makes the Kubernetes API
// the machine API.
//
// An operator is not a special kind of software. It is an ordinary
// program that runs in a pod, reads the state of the world, compares
// it to a declared spec, and acts until they agree, then keeps
// watching, forever. Kubernetes itself is built out of these loops
// (kube-controller-manager alone runs dozens of them); this one just
// happens to reconcile the machine underneath the cluster instead of
// something inside it.
//
// liken's OS is operated by two programs, split by what they stand
// on. This one is node-local: it runs privileged on every machine (a
// DaemonSet), reads the facts init published, actuates the spec
// against the machine itself, and reports the result as its
// Machine's status. Its counterpart, liken-cluster-operator, is an
// ordinary unprivileged workload that watches the whole fleet and
// writes the verdicts no single machine can: which machines are
// Lost, the Cluster's headcount, whose turn it is to reboot. The
// seam between them is the Machine status this program writes and
// the heartbeat lease it renews.
//
// The division of labor with init (see the machine package): init
// observes the boot and writes facts to /run/liken; this operator
// reads them through a hostPath mount, folds in what it can observe
// itself, and publishes the result as the Machine's status. In the
// other direction it actuates the spec: today that means sysctls,
// written straight to /proc/sys, which is the host's, because this
// pod runs privileged in the host's namespaces (see
// manifests/machine-operator.yaml for what that means and why it's
// justified here).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

func main() {
	fmt.Println("liken-machine-operator", machine.Version)

	// The boot manifest tells the operator which Machine it manages:
	// the exact bytes init booted under (the proven or staged copy
	// from machineState, or the image's seed on a first boot),
	// published to /run/liken and read here through a hostPath mount.
	// One image carries manifests for a whole fleet, and init already
	// did the work of selecting this machine's; the operator trusts
	// that selection rather than repeating it.
	m, err := machine.Load(machine.BootManifestPath)
	if err != nil {
		fatal("boot manifest: %v", err)
	}
	name := m.Metadata.Name
	if name == "" {
		fatal("the boot manifest names no machine; nothing to operate")
	}

	// The cluster document this boot ran, read before the client is
	// built because it decides which API endpoint this machine should
	// use (localAPIEndpoint, endpoint.go).
	cluster, err := machine.LoadCluster(machine.ClusterManifestPath)
	if err != nil {
		fatal("cluster manifest: %v", err)
	}

	// Failures during setup end the process deliberately. There is no
	// retry logic here because kubelet already provides it: a pod that
	// exits nonzero is restarted with backoff, and the failure is
	// visible in `kubectl get pods` instead of buried in a log. This is
	// the "crash-only" style most Kubernetes components use.
	client, err := kubernetes.InClusterClientAt(localAPIEndpoint(cluster, name))
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

	// The file seeds the cluster: if no Machine object exists yet, the
	// manifest's spec becomes the first version of it. From then on the
	// cluster's copy is authoritative: a kubectl edit wins over the
	// file until someone rebuilds the image. (A GitOps engine running
	// in the cluster could own the in-cluster copy and converge the two
	// sides for good; that delivery choice belongs to the deployment,
	// not the OS.)
	current, err := ensureMachine(client, m)
	if err != nil {
		fatal("ensuring machine %s exists: %v", name, err)
	}
	fmt.Printf("operating machine %s\n", name)

	// The Cluster resource gets the same treatment as the Machine: the
	// image's cluster.yaml seeds it if it doesn't exist. Every
	// machine's operator tries this, because every image carries the
	// manifest, so most of them lose the race and find the object
	// already there. That still counts as success. What matters is
	// that the cluster's topology is queryable, not which machine
	// published it. (Seeding lives here rather than in the cluster
	// operator because this is the program with the image's manifest
	// under its feet; the cluster operator has no mounts at all.)
	if cluster != nil {
		if err := ensureCluster(client, cluster); err != nil {
			fatal("ensuring cluster %s exists: %v", cluster.Metadata.Name, err)
		}
	}

	// The cluster's name is the operator's handle for reading the live
	// Cluster resource each pass (cluster convergence); a machine with
	// no cluster manifest has no document to converge.
	clusterName := ""
	if cluster != nil {
		clusterName = cluster.Metadata.Name
	}

	// The core of every operator is a level-triggered loop. Watch
	// events tell us *when* to look. The ticker guarantees we look
	// even when nothing happened, because facts can change without the
	// object changing, and there is no event for drift. Every pass
	// reconciles from absolute current state, never from the event
	// that woke us, so that missing one event can never matter.
	//
	// The watch covers exactly one object: this machine's own. The
	// fieldSelector asks the server to filter, so no other machine's
	// write ever crosses the wire to this pod. The rest of the fleet
	// is the cluster operator's concern; nothing in this program's job
	// depends on any Machine but its own, and a five-hundred-machine
	// fleet shouldn't cost every machine five hundred wakeups.
	// Buffered so a burst of writes to this object (the conductor's
	// grant, the sweeper's verdict, our own publishes echoing back)
	// queues up instead of stalling the watch stream; the loop below
	// drains and coalesces whatever accumulated while a pass was
	// working.
	events := make(chan *machine.Machine, 32)
	go kubernetes.WatchMachines(client, "metadata.name="+name, current.Metadata.ResourceVersion, events)

	// The release fetcher outlives any one pass: downloads take
	// minutes, passes take milliseconds, and the fetcher is the one
	// piece of state that bridges them (fetch.go).
	f := &fetcher{}

	// The ticker is the heartbeat's metronome as well as the drift
	// backstop, so it runs at the kubelet's own lease cadence of ten
	// seconds (the kubernetes package explains the numbers). The
	// heartbeat is deliberately renewed by the reconcile pass rather
	// than by a dedicated goroutine: a heartbeat should prove the
	// operator is doing its job, and a goroutine would keep vouching
	// for a reconcile loop that had wedged.
	ticker := time.NewTicker(10 * time.Second)
	for {
		reconcile(client, current, clusterName, f)
		select {
		case m := <-events:
			// A busy object queues events faster than passes run, so
			// take everything that arrived while the last pass worked.
			// One pass over the newest state answers a whole burst;
			// that is what level-triggered means, and it is the same
			// coalescing an informer's work queue does. Skipping
			// intermediate copies also keeps this pass from publishing
			// against a version it already knows is stale.
			current = drainEvents(events, m)
		case <-ticker.C:
			// Re-read the object on timer passes too: status writes bump
			// resourceVersion, and reconciling against a stale copy
			// would make every status update a conflict.
			if refreshed, err := kubernetes.GetMachine(client, name); err == nil {
				current = refreshed
			}
		}
	}
}

// drainEvents empties whatever the watch queued while the last pass
// ran, returning the newest copy of this machine's object. Every
// event on the channel is this machine's own (the watch's
// fieldSelector saw to that), so draining is just keeping the last
// word. Every drained event is answered by the single pass that
// follows.
func drainEvents(events <-chan *machine.Machine, newest *machine.Machine) *machine.Machine {
	for {
		select {
		case m := <-events:
			newest = m
		default:
			return newest
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
