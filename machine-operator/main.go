// liken-machine-operator: the program that makes the Kubernetes API
// the machine API.
//
// An operator is not a special kind of software. It is an ordinary
// program that runs in a pod, reads the state of the world, compares
// it to a declared spec, and acts until they agree. Then it keeps
// watching, forever. Kubernetes itself is built out of these loops
// (kube-controller-manager alone runs dozens of them). This one
// reconciles the machine underneath the cluster, instead of
// something inside it.
//
// liken's OS runs two programs, split by their scope. This one is
// node-local: it runs privileged on every machine (a DaemonSet),
// reads the facts init published, actuates the spec against the
// machine itself, and reports the result as its Machine's status.
// Its counterpart, liken-cluster-operator, is an ordinary
// unprivileged workload that watches the whole fleet and writes the
// verdicts no single machine can make: which machines are Lost, the
// Cluster's headcount, and whose turn it is to reboot. The
// connection between them is the Machine status this program writes
// and the heartbeat lease it renews.
//
// This program divides the work with init (see the machine
// package) as follows. Init observes the boot and writes facts to
// /run/liken. This operator reads them through a hostPath mount,
// adds what it can observe itself, and publishes the result as the
// Machine's status. In the other direction, it actuates the spec.
// Today that means sysctls, written straight to /proc/sys, which
// belongs to the host, because this pod runs privileged in the
// host's namespaces (see manifests/machine-operator.yaml for what
// that means and why it is justified here).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

func main() {
	fmt.Println("liken-machine-operator", machine.Version)

	// The boot manifest tells the operator which Machine it manages.
	// These are the exact bytes init booted under: the proven or
	// staged copy from machineState, or the image's seed on a first
	// boot. Init publishes them to /run/liken, and the operator reads
	// them here through a hostPath mount. One image carries manifests
	// for a whole fleet, and init already selected this machine's.
	// The operator trusts that selection instead of repeating the
	// work.
	m, err := machine.Load(machine.BootManifestPath)
	if err != nil {
		fatal("boot manifest: %v", err)
	}
	name := m.Metadata.Name
	if name == "" {
		fatal("the boot manifest names no machine; nothing to operate")
	}

	// This reads the cluster document that this boot ran, before
	// building the client, because the document decides which API
	// endpoint this machine should use (localAPIEndpoint,
	// endpoint.go).
	clusterDoc, err := cluster.LoadCluster(cluster.ClusterManifestPath)
	if err != nil {
		fatal("cluster manifest: %v", err)
	}

	// Failures during setup end the process deliberately. This code
	// has no retry logic, because the kubelet already provides it: a
	// pod that exits nonzero is restarted with backoff, and the
	// failure is visible in `kubectl get pods` instead of hidden in a
	// log. This is the crash-only style most Kubernetes components
	// use.
	client, err := kubernetes.InClusterClientAt(localAPIEndpoint(clusterDoc, name))
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

	// The DRA plugin serves the kubelet for the life of the process
	// (draplugin.go). Its failure is loud but not fatal, deliberately.
	// Right after an upgrade, this binary can run in a pod created
	// from the previous release's template: OnDelete keeps the old
	// pod, and the stable image tag resolves to the new build. If
	// that template lacks a mount this plugin needs, dying here would
	// kill the whole operator, including the status publishing that
	// the pod steward is waiting on to refresh this same pod. The
	// machine must keep operating without device claims. The
	// refreshed pod brings the plugin up.
	go func() {
		if err := serveDRAPlugin(context.Background(), client); err != nil {
			fmt.Fprintf(os.Stderr, "the DRA plugin is not serving: %v\n", err)
		}
	}()

	// The file seeds the cluster. If no Machine object exists yet,
	// the manifest's spec becomes the first version of it. From then
	// on, the cluster's copy is authoritative: a kubectl edit wins
	// over the file until someone rebuilds the image. (The flux
	// feature closes this loop for good: a deployment that declares
	// it hands the in-cluster copy to its git repository, and the
	// two sides converge on every commit.)
	current, err := ensureMachine(client, m)
	if err != nil {
		fatal("ensuring machine %s exists: %v", name, err)
	}
	fmt.Printf("operating machine %s\n", name)

	// The Cluster resource gets the same treatment as the Machine:
	// the image's cluster.yaml seeds it if it does not exist. Every
	// machine's operator tries this, because every image carries the
	// manifest, so most of them lose the race and find the object
	// already there. That still counts as success. What matters is
	// that the cluster's topology can be read, not which machine
	// published it. (Seeding happens here, rather than in the
	// cluster operator, because this program has the image's manifest
	// available to it. The cluster operator has no mounts at all.)
	if clusterDoc != nil {
		if err := ensureCluster(client, clusterDoc); err != nil {
			fatal("ensuring cluster %s exists: %v", clusterDoc.Metadata.Name, err)
		}
	}

	// The cluster's name is what the operator uses to read the live
	// Cluster resource on each pass (cluster convergence). A machine
	// with no cluster manifest has no document to converge.
	clusterName := ""
	if clusterDoc != nil {
		clusterName = clusterDoc.Metadata.Name
	}

	// The core of every operator is a level-triggered loop. Three
	// things wake it, and every pass reconciles from the current state
	// as it is, never from the event that woke it, so missing one wake
	// can never matter. The Kubernetes watch wakes the loop when this
	// machine's own object changes, so a conductor's grant or a
	// person's edit is acted on at once. The facts watch wakes the loop
	// when init publishes a change under /run/liken/facts, so a fresh
	// fact like a time sync reaches status without waiting on a timer.
	// The ticker wakes the loop on a fixed cadence, so it renews the
	// heartbeat lease and backstops any drift that neither watch
	// reported; it is no longer the bound on facts latency.
	//
	// The watch covers exactly one object: this machine's own. The
	// fieldSelector asks the server to filter, so no other machine's
	// write ever reaches this pod. The rest of the fleet is the
	// cluster operator's concern. Nothing in this program's job
	// depends on any Machine but its own, and a five-hundred-machine
	// fleet should not cost every machine five hundred wakeups. The
	// channel is buffered, so a burst of writes to this object (the
	// conductor's grant, the sweeper's verdict, this operator's own
	// publishes echoing back) queues up instead of stalling the watch
	// stream. The loop below drains and combines whatever built up
	// while a pass was running.
	events := make(chan *machine.Machine, 32)
	go kubernetes.WatchMachines(client, "metadata.name="+name, current.Metadata.ResourceVersion, events)

	// The release fetcher outlives any one pass: downloads take
	// minutes, passes take milliseconds, and the fetcher is the one
	// piece of state that connects them (fetch.go).
	f := &fetcher{}

	// The facts watch turns init's writes into wakes. inotify does not
	// recurse, so the watch reconciles its set with the tree before
	// every read (Sync, below). A watch that cannot start is not fatal:
	// the tree may not exist yet this early, so the operator logs the
	// error, runs on the ticker alone, and retries the watch on a later
	// ticker pass. The cost of a missing watch is latency, never
	// correctness.
	factsWatch, err := machine.WatchFactsTree(context.Background(), machine.FactsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching the facts tree: %v\n", err)
	}

	// The ticker sets the pace for the heartbeat and also acts as the
	// drift backstop, so it runs at the kubelet's own lease cadence
	// of ten seconds (the kubernetes package explains the numbers).
	// The reconcile pass renews the heartbeat deliberately, instead
	// of a dedicated goroutine doing it: a heartbeat should prove the
	// operator is doing its job, and a goroutine would keep
	// confirming a reconcile loop that had gotten stuck.
	ticker := time.NewTicker(10 * time.Second)
	for {
		// Sync before the read closes the window between a new subtree
		// and the watch on it: a directory that init created since the
		// last pass gets a watch now, before this pass reads the tree.
		if factsWatch != nil {
			if err := factsWatch.Sync(); err != nil {
				fmt.Fprintf(os.Stderr, "syncing the facts watch: %v\n", err)
			}
		}
		reconcile(client, current, clusterName, f)
		select {
		case m := <-events:
			// A busy object queues events faster than passes run, so
			// this takes everything that arrived while the last pass
			// worked. One pass over the newest state answers a whole
			// burst. That is what level-triggered means, and it is
			// the same combining an informer's work queue does.
			// Skipping intermediate copies also keeps this pass from
			// publishing against a version it already knows is stale.
			current = drainEvents(events, m)
		case <-factsWake(factsWatch):
			// A fact changed on disk. The next pass rereads the tree;
			// the current object is reused as is, because the publish
			// conflict retry already handles a stale working copy.
		case <-ticker.C:
			// A watch that failed to start earlier gets another try
			// here, once the tree's root is likely to exist.
			if factsWatch == nil {
				if w, werr := machine.WatchFactsTree(context.Background(), machine.FactsDir); werr == nil {
					factsWatch = w
				}
			}
			// This rereads the object on timer passes too. Status
			// writes change resourceVersion, and reconciling against
			// a stale copy would make every status update a conflict.
			if refreshed, err := kubernetes.GetMachine(client, name); err == nil {
				current = refreshed
			}
		}
	}
}

// factsWake returns the facts watch's wake channel, or a nil channel
// when there is no watch. A receive on a nil channel blocks forever, so
// the select arm simply never fires while the watch is down, and the
// ticker drives the passes on its own.
func factsWake(w *machine.TreeWatch) <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.Wake
}

// drainEvents empties whatever the watch queued while the last pass
// ran, and returns the newest copy of this machine's object. Every
// event on the channel is this machine's own, because the watch's
// fieldSelector ensures that, so draining only keeps the newest
// one. The single pass that follows answers every drained event.
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
