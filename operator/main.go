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
	// file until someone rebuilds the image. (One day Flux will manage
	// the in-cluster copy from git and the two sides converge for good.)
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
	events := make(chan *machine.Machine, 1)
	go watchMachine(client, name, current.Metadata.ResourceVersion, events)

	// The release fetcher outlives any one pass: downloads take
	// minutes, passes take milliseconds, and the fetcher is the one
	// piece of state that bridges them (fetch.go).
	f := &fetcher{}

	ticker := time.NewTicker(30 * time.Second)
	for {
		reconcile(client, current, clusterName, f)
		select {
		case m := <-events:
			current = m
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

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
