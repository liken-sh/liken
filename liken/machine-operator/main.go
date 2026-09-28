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
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/machine"
)

// component is this program's name in every metric that carries one.
const component = "liken-machine-operator"

// metricsAddress is where this operator answers a Prometheus scrape.
// The default is the port that plans/completed/65-prometheus-metrics.md gives
// the machine operator, so the binary carries the contract and the
// pod template only has to name the port it exposes. The address is
// an argument, and not a constant, for two reasons. An empty value
// turns the listener off, for an owner who runs no Prometheus and
// wants the port back. This pod also runs on the host's network, so
// the port belongs to the whole machine, and an owner who already
// serves 9200 there needs a way to move liken.
var metricsAddress = flag.String("metrics-address", ":9200",
	"the address to serve /metrics on; empty serves no metrics")

func main() {
	flag.Parse()
	fmt.Println(component, machine.Version)

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
	// building the client, because the document names which API
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
	// The plugin can prepare a claim before the first reconcile pass
	// reads the live spec, so the boot manifest's serio entries, and
	// the entries a live load added since the boot, seed the list of
	// serial lines it withholds (dra.go). Facts that do not read leave
	// the boot manifest's entries alone.
	bootFacts, _ := factsTree.Read()
	setDeclaredSerio(serioInEffect(m.Spec.Serio, bootFacts))
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

	// The release fetcher outlives any one pass: downloads take
	// minutes, passes take milliseconds, and the fetcher is the one
	// piece of state that connects them (fetch.go).
	f := &fetcher{}

	// The metrics registry outlives every pass too, because a
	// counter's whole value is that it accumulates (metrics.go).
	operatorMetrics, machineLayer := serveMetrics(*metricsAddress, f)

	// The core of every operator is a level-triggered loop. Three
	// things wake it, and every pass reconciles from the current state
	// as it is, never from the event that woke it, so missing one wake
	// can never matter. The Kubernetes watches wake the loop when an
	// object this machine acts on changes, so a conductor's grant or a
	// person's edit is acted on at once (watches.go names each watch
	// and what wakes it). The facts watch wakes the loop when init
	// publishes a change under /run/liken/facts, so a fresh fact like
	// a time sync reaches status without waiting on a timer. The
	// ticker wakes the loop on a fixed cadence. It renews the heartbeat
	// lease, and it catches the changes that no watch reports (the
	// ticker's own comment below names them).
	//
	// The wake channel has one slot, so a burst of changes (the
	// conductor's grant, the sweeper's verdict, this operator's own
	// publishes echoing back) makes one wake, and one pass over the
	// newest state answers the whole burst. That is what
	// level-triggered means, and it is the same merging an informer's
	// work queue does.
	watcher, err := informer.InCluster(localAPIEndpoint(clusterDoc, name))
	if err != nil {
		fatal("in-cluster config for the watches: %v", err)
	}
	wakes := make(chan struct{}, 1)
	objects := watchThisMachine(context.Background(), watcher, client, name, clusterName,
		informer.Signal(wakes), operatorMetrics.WatchRestarted)

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

	// The heartbeat outlives every pass, because it holds the lease it
	// last wrote, and a renewal from that copy needs no read
	// (kubernetes/heartbeat.go).
	heartbeat := kubernetes.NewHeartbeat(name)

	// The ticker is a clock first: it sets the pace for the
	// heartbeat, so it runs at the kubelet's own lease cadence of ten
	// seconds (the kubernetes package explains the numbers). The
	// reconcile pass renews the heartbeat deliberately, instead of a
	// dedicated goroutine doing it: a heartbeat should prove the
	// operator is doing its job, and a goroutine would keep
	// confirming a reconcile loop that had gotten stuck.
	//
	// The same pass is also the backstop for the state on the machine
	// that no event announces. A sysctl or an /etc/hosts entry that
	// another process changes sends no event, so the pass writes each
	// one back within ten seconds. The DRA inventory comes from a walk
	// of sysfs on each pass. A release download that finishes between
	// passes reaches status on the next one. The API objects the pass
	// judges come from the watches' copies, so a ticker pass on a
	// settled machine sends one request: the heartbeat's renewal.
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
		// Each pass starts from the newest copy of this machine's
		// object. Status writes change resourceVersion, and
		// reconciling against a stale copy would make every status
		// update a conflict. A read that fails keeps the copy the last
		// pass had; the publish conflict retry handles a stale one.
		//
		// A Machine that is gone gets no heartbeat. The lease names the
		// Machine as its owner, so the garbage collector deletes the
		// lease with it, and a renewal from the last copy would create
		// the lease again, owned by a Machine that does not exist, for
		// the collector to delete again.
		renewing := heartbeat
		if fresh, err := objects.machine(name); err == nil {
			current = fresh
		} else if errors.Is(err, kubernetes.ErrNotFound) {
			renewing = nil
		}
		started := time.Now()
		err := reconcile(objects, current, clusterName, f, renewing, machineLayer)
		operatorMetrics.ObserveReconcile(machineKind, time.Since(started), err)
		select {
		case <-wakes:
		case <-factsWake(factsWatch):
		case <-ticker.C:
			// A watch that failed to start earlier gets another try
			// here, once the tree's root is likely to exist.
			if factsWatch == nil {
				if w, werr := machine.WatchFactsTree(context.Background(), machine.FactsDir); werr == nil {
					factsWatch = w
				}
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

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
