// liken-operator: the program that makes the Kubernetes API the
// machine API.
//
// An operator is not a special kind of software. It is an ordinary
// program that runs in a pod, reads the state of the world, compares
// it to a declared spec, and acts until they agree, then keeps
// watching, forever. Kubernetes itself is built out of these loops
// (kube-controller-manager alone runs dozens of them); this one just
// happens to reconcile the machine underneath the cluster instead of
// something inside it.
//
// This operator is written against the Kubernetes API with nothing
// but net/http and encoding/json: no client-go, no
// controller-runtime, no code generation. Production operators use
// those libraries for good reasons (caching informers, work queues,
// generated typed clients), but they also hide the lesson: the
// Kubernetes API is just HTTPS serving JSON, a watch is just a long
// HTTP response that keeps coming, and everything kubectl does, curl
// can do. liken speaks DHCP directly for the same reason.
//
// The division of labor with init (see the machine package): init
// observes the boot and writes facts to /run/liken; this operator
// reads them through a hostPath mount, folds in what it can observe
// itself, and publishes the result as the Machine's status. In the
// other direction it actuates the spec: today that means sysctls,
// written straight to /proc/sys, which is the host's, because this pod
// runs privileged in the host's namespaces (see manifests/operator.yaml
// for what that means and why it's justified here).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/chrisguidry/liken/machine"
)

func main() {
	fmt.Println("liken-operator", machine.Version)

	// Failures during setup end the process deliberately. There is no
	// retry logic here because kubelet already provides it: a pod that
	// exits nonzero is restarted with backoff, and the failure is
	// visible in `kubectl get pods` instead of buried in a log. This is
	// the "crash-only" style most Kubernetes components use.
	client, err := inClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

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
	// published it.
	cluster, err := machine.LoadCluster(machine.ClusterManifestPath)
	if err != nil {
		fatal("cluster manifest: %v", err)
	}
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
	// The watch spans every Machine, not just this one. The Cluster's
	// status is derived from the whole fleet by the sweeping leader
	// (fleet.go), and the sweep runs at the end of a reconcile pass,
	// so if only a ticker noticed another machine turning Ready, the
	// Cluster would go on reporting Degraded for up to a full tick
	// after the fleet had recovered. The level-triggered design is
	// what makes the wider watch safe: an extra pass observes and
	// re-asserts the same state. It is also what keeps it quiet. A
	// settled machine's status rewrite is byte-identical, the API
	// server drops identical writes without bumping resourceVersion,
	// and a write that isn't an event can't wake anyone, so operators
	// watching each other cannot echo.
	// Buffered so a fleet-wide burst of events queues up instead of
	// stalling the watch stream; the loop below drains and coalesces
	// whatever accumulated while a pass was working.
	events := make(chan *machine.Machine, 32)
	go watchMachines(client, name, current.Metadata.ResourceVersion, events)

	// The release fetcher outlives any one pass: downloads take
	// minutes, passes take milliseconds, and the fetcher is the one
	// piece of state that bridges them (fetch.go).
	f := &fetcher{}

	// The ticker is the heartbeat's metronome as well as the drift
	// backstop, so it runs at the kubelet's own lease cadence of ten
	// seconds (fleet.go explains the numbers). The heartbeat is
	// deliberately renewed by the reconcile pass rather than by a
	// dedicated goroutine: a heartbeat should prove the operator is
	// doing its job, and a goroutine would keep vouching for a
	// reconcile loop that had wedged.
	ticker := time.NewTicker(10 * time.Second)
	for {
		reconcile(client, current, clusterName, f)
		select {
		case m := <-events:
			// Only this machine's own copy replaces the working copy;
			// another machine's event is just a reason to look again,
			// and the pass reads whatever fleet state it needs fresh.
			if m.Metadata.Name == name {
				current = m
			}
			// A busy fleet queues events faster than passes run, so
			// take everything that arrived while the last pass worked.
			// One pass over the newest state answers a whole burst;
			// that is what level-triggered means, and it is the same
			// coalescing an informer's work queue does. Skipping
			// intermediate copies also keeps this pass from publishing
			// against a version it already knows is stale.
			current = drainEvents(events, name, current)
		case <-ticker.C:
			// Re-read the object on timer passes too: status writes bump
			// resourceVersion, and reconciling against a stale copy
			// would make every status update a conflict.
			if refreshed, err := getMachine(client, name); err == nil {
				current = refreshed
			}
		}
	}
}

// drainEvents empties whatever the watch queued while the last pass
// ran, returning the newest copy of this machine's own object (or
// the given one, when only other machines changed). Every drained
// event is answered by the single pass that follows.
func drainEvents(events <-chan *machine.Machine, name string, current *machine.Machine) *machine.Machine {
	for {
		select {
		case m := <-events:
			if m.Metadata.Name == name {
				current = m
			}
		default:
			return current
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
