// liken-machine-operator reconciles one machine with its Machine
// resource. It runs as a privileged DaemonSet pod on every machine.
// It reads the facts that init wrote to /run/liken, adds what it
// observes itself, and publishes the result as the Machine's status.
// In the other direction it applies the Machine's spec to the host.
// For sysctls that means writing /proc/sys, which works because the
// pod runs privileged in the host's namespaces (see
// manifests/machine-operator.yaml).
//
// liken-cluster-operator is the other operator. It runs as an
// unprivileged workload, watches every Machine, and writes what no
// single machine can decide: which machines are Lost, the Cluster's
// status, and whose turn it is to reboot. The two communicate through
// the Machine status that this program writes and the heartbeat lease
// that it renews.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
)

// component is this program's name, which liken_build_info reports
// in its component label.
const component = "liken-machine-operator"

// An empty metricsAddress turns the listener off, for a cluster with
// no Prometheus. The pod uses the host network, so the port is the
// machine's, and a machine that already serves something on 9200 needs
// a way to move it.
var metricsAddress = flag.String("metrics-address", ":9200",
	"the address that serves /metrics. An empty value turns the listener off")

func main() {
	flag.Parse()
	fmt.Println(component, machine.Version)

	// The boot manifest is the Machine manifest that init booted with,
	// copied to /run/liken: the proven or staged copy from
	// machineState, or on a first boot the image's manifest for this
	// machine. Init has already picked this machine's manifest from the
	// image, so the operator does not pick again. After a rollback the
	// proven manifest can come from a newer release, so LoadKnown skips
	// the fields that this release does not define, as init does.
	m, ignored, err := machine.LoadKnown(machine.BootManifestPath)
	if err != nil {
		fatal("reading the boot manifest: %v", err)
	}
	if len(ignored) > 0 {
		fmt.Printf("skipping the boot manifest fields that this release does not know: %s\n", strings.Join(ignored, ", "))
	}
	name := m.Metadata.Name
	if name == "" {
		fatal("the boot manifest has no metadata.name, so there is no machine to operate")
	}

	// The cluster document is read before the client is built, because
	// it decides which API endpoint the client uses (localAPIEndpoint,
	// endpoint.go).
	clusterDoc, err := cluster.LoadCluster(cluster.ClusterManifestPath)
	if err != nil {
		fatal("reading the cluster manifest: %v", err)
	}

	// A failure during setup exits the process without a retry. The
	// kubelet restarts a pod that exits nonzero with backoff, and the
	// restarts show in `kubectl get pods`.
	client, err := kubernetes.InClusterClient(localAPIEndpoint(clusterDoc, name))
	if err != nil {
		fatal("building the in-cluster client: %v", err)
	}

	// The DRA plugin serves the kubelet for the life of the process
	// (draplugin.go). When it fails to start, the operator logs the
	// error and keeps running. The DaemonSet updates OnDelete and its
	// image tag `installed` names each release's build, so after an
	// upgrade this binary can run in a pod made from the previous
	// release's template, which can lack a mount the plugin needs.
	// Exiting would also stop the status writes that cluster-operator reads
	// to decide when to delete the outdated pod
	// (cluster-operator/steward.go). Without the plugin the machine
	// loses only device claims, until the new pod starts the plugin.
	//
	// The kubelet can ask the plugin to prepare a claim before the
	// first pass reads the live spec. So before the plugin starts,
	// main sets the serial lines that it must not hand to a claim. They
	// come from the boot manifest's spec.serio and from the facts' boot
	// record, which also lists the entries that a live load attached
	// since boot (dra.go). When the facts cannot be read, they come from
	// the boot manifest alone.
	bootFacts, _ := factsTree.Read()
	setDeclaredSerio(serioInEffect(m.Spec.Serio, bootFacts))
	setPlatformProtection(protectionOf(bootFacts))
	// Unpreparing a claim wakes the loop, so the ResourceSlice drops
	// the taint of the claim that ended (unplugged.go).
	released := make(chan struct{}, 1)
	go func() {
		if err := serveDRAPlugin(context.Background(), client, released); err != nil {
			fmt.Fprintf(os.Stderr, "the DRA plugin is not serving: %v\n", err)
		}
	}()

	// The recorder writes Events from its own goroutine, so a pass
	// never waits for an Event (events.go).
	recorder := events.New(context.Background(), client, component, events.Options{})

	// When no Machine object exists yet, ensureMachine creates it from
	// the boot manifest. After that the object in the cluster wins, so
	// a `kubectl edit` takes precedence over the manifest until the
	// image is rebuilt. With the flux feature, the object's spec comes
	// from the git repository instead.
	current, err := ensureMachine(client, m, recorder)
	if err != nil {
		fatal("ensuring machine %s exists: %v", name, err)
	}
	fmt.Printf("operating machine %s\n", name)

	// The Cluster resource is created from the image's cluster.yaml
	// when it does not exist. Every machine's operator tries, because
	// every image has the manifest, and an operator that loses the race
	// finds the object already there, which counts as success. The
	// cluster operator's pod mounts no volumes, so it cannot read the
	// manifest.
	if clusterDoc != nil {
		if err := ensureCluster(client, clusterDoc); err != nil {
			fatal("ensuring cluster %s exists: %v", clusterDoc.Metadata.Name, err)
		}
	}

	// Each pass reads the live Cluster resource by this name. A machine
	// with no cluster manifest has no Cluster to converge with.
	clusterName := ""
	if clusterDoc != nil {
		clusterName = clusterDoc.Metadata.Name
	}

	// A release download takes minutes and a pass takes milliseconds,
	// so the fetcher keeps its state across passes (fetch.go).
	f := &fetcher{}

	// The metrics registry also lives for the whole process, because
	// counters accumulate across passes (metrics.go). The liveness
	// check fails when the loop has been busy too long. The renewal
	// timer then exits the process, and the kubelet starts it again
	// (liveness.go).
	live := newLiveness()
	operatorMetrics, machineLayer := serveMetrics(*metricsAddress, f, live.check)

	watcher, err := informer.InClusterAt(localAPIEndpoint(clusterDoc, name))
	if err != nil {
		fatal("building the in-cluster client for the watches: %v", err)
	}
	// The wake channel has a buffer of one, so a burst of changes
	// queues a single wake (loop.go).
	wakes := make(chan struct{}, 1)
	f.wake = watch.Signal(wakes)
	objects := watchThisMachine(context.Background(), watcher, client, name, clusterName,
		watch.Signal(wakes), operatorMetrics.WatchRestarted)
	objects.recorder = recorder

	// These event sources open before the first pass, so a change that
	// happens during that pass wakes the loop after it
	// (machineevents.go). A pod from a template that predates the
	// /host/etc mount has no directory to watch. The operator then
	// runs without the hosts watch, and hostEntriesCondition reports
	// the missing mount.
	uevents, err := listenForUevents(context.Background())
	if err != nil {
		fatal("listening for uevents: %v", err)
	}
	hosts, err := watchHostsFile(context.Background())
	if err := hostsWatchFailure(err); err != nil {
		fatal("watching %s: %v", hostsPath, err)
	} else if hosts == nil {
		fmt.Fprintf(os.Stderr, "watching %s: the directory does not exist\n", hostsPath)
	}

	l := &loop{
		objects:     objects,
		name:        name,
		clusterName: clusterName,
		fetcher:     f,
		// The heartbeat keeps the lease it last wrote, so a renewal
		// needs no read (kubernetes/heartbeat.go).
		heartbeat: kubernetes.NewHeartbeat(name),
		operator:  operatorMetrics,
		layer:     machineLayer,
		wakes:     wakes,
		uevents:   uevents,
		hosts:     hosts,
		released:  released,
		live:      live,
		watchFactsTree: func(ctx context.Context) (*factsWatch, error) {
			w, err := machine.WatchFactsTree(ctx, machine.FactsDir)
			if err != nil {
				return nil, err
			}
			return &factsWatch{wake: w.Wake, sync: w.Sync}, nil
		},
	}
	if err := l.run(context.Background(), current); err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
