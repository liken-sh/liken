// liken-operator — the program that makes the Kubernetes API the
// machine API.
//
// An operator is not a special kind of software. It is an ordinary
// program that runs in a pod, reads the state of the world, compares it
// to a declared spec, and acts until they agree — then keeps watching,
// forever. Kubernetes itself is a building full of these loops
// (kube-controller-manager is dozens of them in a trenchcoat); this one
// just happens to reconcile the machine underneath the cluster instead
// of something inside it.
//
// This operator is written against the Kubernetes API with nothing but
// net/http and encoding/json — no client-go, no controller-runtime, no
// code generation. Production operators use those libraries for good
// reasons (caching informers, work queues, generated typed clients),
// but they also bury the lesson: the Kubernetes API is just HTTPS
// serving JSON, a watch is just a long HTTP response that keeps coming,
// and everything kubectl does, curl can do. liken hand-rolled its DHCP
// client for the same reason.
//
// The division of labor with init (see the machine package): init
// witnesses the boot and writes facts to /run/liken; this operator
// reads them through a hostPath mount, folds in what it can observe
// itself, and publishes the result as the Machine's status. In the
// other direction it actuates the spec: today that means sysctls,
// written straight to /proc/sys — which is the host's, because this pod
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

	// Failures during setup end the process, on purpose. There is no
	// retry logic here because kubelet *is* the retry logic: a pod that
	// exits nonzero is restarted with backoff, and the failure is
	// visible in `kubectl get pods` instead of buried in a log. This is
	// the "crash-only" style most Kubernetes components use.
	client, err := inClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}

	// The manifest file tells the operator which Machine it is — the
	// same file init booted from, read through a hostPath mount.
	m, err := machine.Load(machine.ManifestPath)
	if err != nil {
		fatal("machine manifest: %v", err)
	}
	name := m.Metadata.Name
	if name == "" {
		fatal("machine manifest names no machine; nothing to operate")
	}

	// The file seeds the cluster: if no Machine object exists yet, the
	// manifest's spec becomes the first version of it. From then on the
	// cluster's copy is authoritative — a kubectl edit wins over the
	// file until someone rebuilds the image. (One day Flux will manage
	// the in-cluster copy from git and the two sides converge for good.)
	current, err := ensureMachine(client, m)
	if err != nil {
		fatal("ensuring machine %s exists: %v", name, err)
	}
	fmt.Printf("operating machine %s\n", name)

	// The core of every operator: a level-triggered loop. Watch events
	// tell us *when* to look; the ticker guarantees we look even when
	// nothing happened (facts can change without the object changing,
	// and drift doesn't announce itself); and every pass reconciles from
	// absolute current state, never from the event that woke us. Missing
	// one event must never matter.
	events := make(chan *machine.Machine, 1)
	go watchMachine(client, name, current.Metadata.ResourceVersion, events)

	ticker := time.NewTicker(30 * time.Second)
	for {
		reconcile(client, current)
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
