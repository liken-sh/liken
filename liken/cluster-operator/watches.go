package main

// The cluster operator's watches, and the reads a sweep makes through
// them.
//
// A sweep judges the whole fleet: every Machine, every heartbeat Lease,
// the Cluster, the OS DaemonSets in liken-system, and the pods of the
// two stewarded DaemonSets. The sweep runs at least every ten seconds,
// because the ticker is the clock that ages heartbeats into Lost
// verdicts and granted turns into a stalled rollout. A sweep that read
// each of these from the API server would send about ten requests
// every ten seconds, and one more full sweep for every status write in
// the fleet. So the operator watches each collection, keeps a copy in
// memory, and the sweep reads the copies.
//
// A watch also decides whether a change wakes the loop at once:
//
//   - A Machine wakes the loop on every change. Any machine's
//     transition can change the Cluster's phase, a rollout's budget,
//     or a Lost verdict, and those are status writes.
//   - The Cluster wakes the loop only on an edit to its spec. This
//     program is the only writer of the Cluster's status, and its own
//     write must not start another sweep.
//   - A DaemonSet wakes the loop only on an edit to its spec. A
//     leader's boot writes the new release's template, and the steward
//     refreshes pods from it. The DaemonSet controller writes status
//     all the time, and none of it concerns the sweep.
//   - The heartbeat Leases and the pods wake nothing. The ticker is the
//     clock that judges a heartbeat's age, and the steward acts on a
//     machine's version, which arrives as a Machine change.
//
// A copy that cannot answer, because its watch has not synced or the
// API server refuses the watch, is never read as the truth. The sweep
// reads the API server instead. That covers the first seconds after
// this process takes the lead, and a release skew, where this binary
// runs under the previous release's RBAC.
//
// Two reads stay direct on every sweep. The flux deploy key Secret is
// read only while the flux feature is declared, and the permission to
// read it arrives with the feature's own manifests, so a watch would
// be refused on every fleet without GitOps. The flux-system Namespace
// is read only while the feature is not declared, to find an
// installation to tear down, and this program may read that one
// Namespace by name only.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/machine"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The kinds this operator watches, as the dynamic client names them.
var (
	likenVersion      = schema.GroupVersion{Group: "liken.sh", Version: strings.TrimPrefix(api.APIVersion, "liken.sh/")}
	machineResource   = likenVersion.WithResource("machines")
	clusterResource   = likenVersion.WithResource("clusters")
	leaseResource     = schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}
	daemonSetResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	podResource       = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// The kind labels of liken_watch_restarts_total, one for each watch.
const (
	leaseKind     = "Lease"
	daemonSetKind = "DaemonSet"
	podKind       = "Pod"
)

// watchKinds lists every kind label, so the counter reports a zero for
// each watch before its first restart.
var watchKinds = []string{machineKind, clusterKind, leaseKind, daemonSetKind, podKind}

// appLabel is the label each stewarded DaemonSet puts on its pods. The
// pod copy is indexed by it, so the steward reads one DaemonSet's pods
// without a scan.
const appLabel = "app"

// fleetReader reads each API object a sweep judges: from the copy a
// watch keeps, or from the API server when the copy cannot answer. A
// fleetReader with no copies at all reads the API server every time,
// which is what the tests of a sweep use.
type fleetReader struct {
	client *kubernetes.Client

	machineCopy   *informer.Collection
	clusterCopy   *informer.Collection
	leaseCopy     *informer.Collection
	daemonSetCopy *informer.Collection
	podCopy       *informer.Collection
}

// watchFleet opens the watches and returns the reader over their
// copies. Each change that needs a sweep calls wake.
func watchFleet(ctx context.Context, watcher dynamic.Interface, client *kubernetes.Client,
	wake func(), restarted func(kind string)) *fleetReader {
	start := func(kind string, source informer.Source, handler cache.ResourceEventHandler, indexers cache.Indexers) *informer.Collection {
		return informer.Start(ctx, watcher, source, informer.Options{
			Handler:  handler,
			Synced:   wake,
			Reopened: func() { restarted(kind) },
			Indexers: indexers,
		})
	}
	machines := informer.Source{Resource: machineResource}
	clusters := informer.Source{Resource: clusterResource}
	leases := informer.Source{Resource: leaseResource, Namespace: "liken-system"}
	daemonSets := informer.Source{Resource: daemonSetResource, Namespace: "liken-system"}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: appLabel + " in (" + strings.Join(stewardedDaemonSets, ",") + ")"}

	return &fleetReader{
		client:        client,
		machineCopy:   start(machineKind, machines, informer.WakeOnChange[machine.Machine](machines, wake), nil),
		clusterCopy:   start(clusterKind, clusters, informer.WakeOnEdit[cluster.Cluster](clusters, wake), nil),
		leaseCopy:     start(leaseKind, leases, nil, nil),
		daemonSetCopy: start(daemonSetKind, daemonSets, informer.WakeOnEdit[featureWorkload](daemonSets, wake), nil),
		podCopy:       start(podKind, pods, nil, cache.Indexers{appLabel: informer.LabelIndex(appLabel)}),
	}
}

// current answers a copy the sweep may read, or nil when the copies
// may be behind the API server.
//
// Every watch of this process shares one HTTP/2 connection. When the
// API server behind it loses power or leaves the network, the stream
// goes quiet, and client-go closes it only about 45 seconds after the
// last frame. The copies look current the whole time. A sweep that
// judged heartbeats from them would find every machine's heartbeat
// aging, and mark live machines Lost after 40 seconds, through a
// write that reaches a live API server on another connection.
//
// The copies carry their own proof of freshness. The leader election
// renews this program's own Lease in liken-system every five seconds,
// and the Leases' copy receives each renewal. When the copy's view of
// that renewal is older than one renewal deadline and one retry period,
// fifteen seconds, the watches or the renewals have stopped, and the
// sweep reads the API server instead. A heartbeat then ages at most
// fifteen seconds in the copy, well inside its 40. client-go gives the
// election and the watches one shared connection, so a stalled
// connection stops the renewals too: the write guard then refuses every
// write after ten seconds, and the process exits when the election
// gives up.
func (r *fleetReader) current(held *informer.Collection) *informer.Collection {
	if held == nil {
		return nil
	}
	lease, found, ok := informer.Get[kubernetes.Lease](r.leaseCopy, leaseNamespace+"/"+leaseName)
	if !ok || !found {
		return nil
	}
	renewed, err := time.Parse(time.RFC3339Nano, lease.Spec.RenewTime)
	if err != nil || time.Since(renewed) >= copyFreshness {
		return nil
	}
	return held
}

// copyFreshness is how old the copy's view of this program's own
// leader Lease may be before the sweep stops reading the copies.
var copyFreshness = operatorLeaseTiming.renewDeadline + operatorLeaseTiming.retryPeriod

// machines reads every Machine in the fleet. The copy cannot answer
// while it lacks one of this program's own writes, such as a grant the
// last sweep wrote, and the sweep then reads the API server, which
// holds the write. A sweep that decided from a copy without its own
// grant would count one fewer machine in flight (kubernetes/informer's
// writes.go).
func (r *fleetReader) machines() ([]machine.Machine, error) {
	if machines, ok := informer.List[machine.Machine](r.current(r.machineCopy)); ok {
		return machines, nil
	}
	machines, err := kubernetes.ListMachines(r.client)
	for _, m := range machines {
		r.machineCopy.Observed(m.Metadata.Name, m.Metadata.ResourceVersion)
	}
	return machines, err
}

// publishStatus writes a Machine's status, and records the write for
// the Machines' copy.
func (r *fleetReader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	version, err := kubernetes.PublishStatus(r.client, m, status)
	recordWrite(r.machineCopy, m.Metadata.Name, version, err)
	return err
}

// publishClusterStatus writes the Cluster's status, and records the
// write for the Clusters' copy, so the next sweep does not compare its
// verdict against a status older than its own last write.
func (r *fleetReader) publishClusterStatus(clusterDoc *cluster.Cluster) error {
	version, err := kubernetes.PublishClusterStatus(r.client, clusterDoc)
	recordWrite(r.clusterCopy, clusterDoc.Metadata.Name, version, err)
	return err
}

// recordWrite tells a copy about one write. A conflict, a 404, and a
// write the guard refused wrote nothing. Any other failure leaves the
// outcome unknown: a request that timed out can still have landed.
func recordWrite(held *informer.Collection, key, version string, err error) {
	switch {
	case err == nil:
		held.Wrote(key, version)
	case errors.Is(err, errNotLeading), errors.Is(err, kubernetes.ErrNotFound):
		// The guard sent nothing, and a 404 wrote nothing to an
		// object that no longer exists, and whose removal the watch
		// has delivered or will deliver.
	case !errors.Is(err, kubernetes.ErrConflict):
		held.Wrote(key, "")
	}
}

// clusters reads every Cluster. A fleet has one.
func (r *fleetReader) clusters() ([]cluster.Cluster, error) {
	if clusters, ok := informer.List[cluster.Cluster](r.current(r.clusterCopy)); ok {
		return clusters, nil
	}
	return kubernetes.ListClusters(r.client)
}

// cluster reads the Cluster this program operates.
func (r *fleetReader) cluster(name string) (*cluster.Cluster, error) {
	if c, found, ok := informer.Get[cluster.Cluster](r.current(r.clusterCopy), name); ok {
		if !found {
			return nil, kubernetes.ErrNotFound
		}
		return c, nil
	}
	c, err := kubernetes.GetCluster(r.client, name)
	if err == nil {
		r.clusterCopy.Observed(name, c.Metadata.ResourceVersion)
	}
	return c, err
}

// heartbeats reads every machine's last renewal.
func (r *fleetReader) heartbeats() (map[string]time.Time, error) {
	if leases, ok := informer.List[kubernetes.Lease](r.current(r.leaseCopy)); ok {
		return kubernetes.Renewals(leases), nil
	}
	return kubernetes.ListHeartbeats(r.client)
}

// workloads reads one kind of workload in liken-system for the feature
// janitor. The DaemonSets come from their copy, which the steward
// reads too.
func (r *fleetReader) workloads(listPath string) ([]featureWorkload, error) {
	if listPath == daemonSetsPath {
		if daemonSets, ok := informer.List[featureWorkload](r.current(r.daemonSetCopy)); ok {
			return daemonSets, nil
		}
	}
	return kubernetes.List[featureWorkload](r.client, listPath)
}

// daemonSet reads one DaemonSet in liken-system.
func (r *fleetReader) daemonSet(name string) (*featureWorkload, error) {
	if ds, found, ok := informer.Get[featureWorkload](r.current(r.daemonSetCopy), "liken-system/"+name); ok {
		if !found {
			return nil, kubernetes.ErrNotFound
		}
		return ds, nil
	}
	ds := &featureWorkload{}
	if err := r.client.RequestJSON(http.MethodGet, daemonSetsPath+"/"+name, nil, ds); err != nil {
		return nil, err
	}
	return ds, nil
}

// daemonSetPods reads the pods of one stewarded DaemonSet.
func (r *fleetReader) daemonSetPods(name string) ([]kubernetes.Pod, error) {
	if pods, ok := informer.ByIndex[kubernetes.Pod](r.current(r.podCopy), appLabel, name); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, daemonSetPodsPath(name))
}
