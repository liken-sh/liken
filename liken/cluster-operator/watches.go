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
// A copy that cannot answer, because its watch has not synced or its
// last watch failed, is never read as the truth. The sweep reads the
// API server instead. That covers the first seconds after this process
// takes the lead, a release skew, where this binary runs under the
// previous release's RBAC, and an API server restart. After a failed
// watch, the reflector waits out a backoff of up to a minute before it
// watches again, and a copy then misses each write made in that time,
// such as a machine's Degraded status, which the budget must count.
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
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
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
	client *apiclient.Client

	// recorder posts the Events about the Machines and the Cluster
	// (events.go). It is here because every part of a sweep that acts
	// reads through the fleet reader. A nil recorder posts nothing.
	recorder *events.Recorder

	machineCopy   *informer.Collection
	clusterCopy   *informer.Collection
	leaseCopy     *informer.Collection
	daemonSetCopy *informer.Collection
	podCopy       *informer.Collection

	// machineVersions and clusterVersions are the memos of this
	// program's own status writes to the Machines and the Cluster, and
	// of its reads of them from the API server (kubernetes/memo). The
	// watch delivers a write a moment after the API server answers it.
	// A sweep that decided from a copy without its own last grant would
	// count one fewer machine in flight, and could grant a turn beyond
	// the disruption budget. So a copy answers only at the version of
	// this program's last write or read of the object, and the sweep
	// otherwise reads that one object from the API server.
	machineVersions *memo.Versions
	clusterVersions *memo.Versions
}

// watchFleet opens the watches and returns the reader over their
// copies. Each change that needs a sweep calls wake.
func watchFleet(ctx context.Context, watcher dynamic.Interface, client *apiclient.Client,
	wake func(), restarted func(kind string)) *fleetReader {
	start := func(kind string, source informer.Source, handler cache.ResourceEventHandler, indexers cache.Indexers) *informer.Collection {
		return informer.Start(ctx, watcher, source, informer.Options{
			Handler:  handler,
			Synced:   wake,
			Reopened: func() { restarted(kind) },
			// A copy stops answering after any failed watch, as the
			// head of this file says.
			UnreadyOnWatchError: true,
			Indexers:            indexers,
		})
	}
	machines := informer.Source{Resource: machineResource}
	clusters := informer.Source{Resource: clusterResource}
	leases := informer.Source{Resource: leaseResource, Namespace: "liken-system"}
	daemonSets := informer.Source{Resource: daemonSetResource, Namespace: "liken-system"}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: appLabel + " in (" + strings.Join(stewardedDaemonSets, ",") + ")"}

	return &fleetReader{
		client:          client,
		machineCopy:     start(machineKind, machines, watch.WakeOnChange[machine.Machine](machines, wake), nil),
		clusterCopy:     start(clusterKind, clusters, watch.WakeOnEdit[cluster.Cluster](clusters, wake), nil),
		leaseCopy:       start(leaseKind, leases, nil, nil),
		daemonSetCopy:   start(daemonSetKind, daemonSets, watch.WakeOnEdit[featureWorkload](daemonSets, wake), nil),
		podCopy:         start(podKind, pods, nil, cache.Indexers{appLabel: watch.LabelIndex(appLabel)}),
		machineVersions: memo.New(),
		clusterVersions: memo.New(),
	}
}

// current answers the view of a copy the sweep may read, or a view
// that answers nothing when the copies may be behind the API server.
//
// Every watch of this process shares one HTTP/2 connection. When the
// API server behind it loses power or leaves the network, the stream
// goes quiet, and client-go closes it only about 45 seconds after the
// last frame. The copies look current the whole time. A sweep that
// judged heartbeats from them would find every machine's heartbeat
// aging, and mark live machines Lost after 40 seconds, through a
// write that reaches a live API server on another connection.
//
// The Leases' copy carries its own proof of freshness. The leader
// election renews this program's own Lease in liken-system every five
// seconds, and the Leases' copy receives each renewal. When the copy's
// view of that renewal is older than one renewal deadline and one retry
// period, fifteen seconds, the stream or the renewals have stopped, and
// the sweep reads the API server instead. A heartbeat then ages at most
// fifteen seconds in the copy, well inside its 40. client-go gives the
// election and the watches one shared connection, so a stalled
// connection stops the renewals too: the write guard then refuses every
// write after ten seconds, and the process exits when the election
// gives up.
//
// That proof covers a quiet connection, not a failed watch. Each
// reflector recovers from a failed watch on its own backoff, so a fresh
// Leases' copy says nothing about the Machines' copy. A failed watch
// makes its own copy stop answering instead
// (informer.Options.UnreadyOnWatchError), and this check comes on top
// of that.
//
// A copy that holds every object of its kind, with no selector, is
// Whole, so a list from it also reads each object this program wrote
// that the copy does not hold yet.
func (r *fleetReader) current(held *informer.Collection) informer.View {
	if held == nil {
		return informer.View{}
	}
	lease, found, ok := watch.Get[kubernetes.Lease](r.leaseCopy.View(), leaseNamespace+"/"+leaseName)
	if !ok || !found {
		return informer.View{}
	}
	renewed, err := time.Parse(time.RFC3339Nano, lease.Spec.RenewTime)
	if err != nil || time.Since(renewed) >= copyFreshness {
		return informer.View{}
	}
	view := held.View()
	view.Whole = held == r.machineCopy || held == r.clusterCopy
	return view
}

// copyFreshness is how old the copy's view of this program's own
// leader Lease may be before the sweep stops reading the copies.
var copyFreshness = operatorLeaseTiming.renewDeadline + operatorLeaseTiming.retryPeriod

// machines reads every Machine in the fleet. A copy at another version
// than this program's own last write or read of the Machine, such as a
// copy without the grant the last sweep wrote, is read again from the
// API server, which holds the write. While the copy cannot answer, the
// list comes from the API server and notes each Machine's version, so a
// copy that answers later at an older version is read again.
func (r *fleetReader) machines() ([]machine.Machine, error) {
	held := informer.Held{View: r.current(r.machineCopy), Versions: r.machineVersions}
	machines, err := informer.List[machine.Machine](r.client, held, kubernetes.MachinesPath, machinePath)
	if err != nil {
		return nil, err
	}
	settleMemo(held, machines)
	return machines, nil
}

func machinePath(name string) string { return kubernetes.MachinesPath + "/" + name }

func clusterPath(name string) string { return kubernetes.ClustersPath + "/" + name }

// settleMemo drops the memo's record of each object that the list left
// out and the store no longer holds: a Machine somebody deleted, whose
// record would otherwise stay for the life of the process. It also
// drops the record of each object whose copy in the store is at the
// noted version (watch.Settle), so a later write from another writer
// costs no read. A list from the API server settles nothing, because
// no ready store compares with it. The records that list noted stay
// until a ready store holds their versions.
func settleMemo[T any, P informer.Object[T]](held informer.Held, items []T) {
	if !held.View.Ready() {
		return
	}
	listed := make(map[string]bool, len(items))
	keys := make([]string, 0, len(items))
	for i := range items {
		key := informer.Key(P(&items[i]).GetObjectMeta())
		listed[key] = true
		keys = append(keys, key)
	}
	held.Versions.ForgetGone(held.View.Store, listed)
	watch.Settle(held, keys...)
}

// publishStatus writes a Machine's status, and notes the version the
// API server answered for the Machines' copy. A write that fails notes
// that this program holds no current copy: a request that timed out can
// still have landed, so the next read of that Machine goes to the API
// server.
func (r *fleetReader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	return r.machineVersions.Send(m.Metadata.Name, func() (string, error) {
		return kubernetes.PublishStatus(r.client, m, status)
	})
}

// publishClusterStatus writes the Cluster's status, and notes the
// version for the Clusters' copy, so the next sweep does not compare
// its verdict against a status older than its own last write.
func (r *fleetReader) publishClusterStatus(clusterDoc *cluster.Cluster) error {
	return r.clusterVersions.Send(clusterDoc.Metadata.Name, func() (string, error) {
		return kubernetes.PublishClusterStatus(r.client, clusterDoc)
	})
}

// clusters reads every Cluster. A fleet has one.
func (r *fleetReader) clusters() ([]cluster.Cluster, error) {
	held := informer.Held{View: r.current(r.clusterCopy), Versions: r.clusterVersions}
	clusters, err := informer.List[cluster.Cluster](r.client, held, kubernetes.ClustersPath, clusterPath)
	if err != nil {
		return nil, err
	}
	settleMemo(held, clusters)
	return clusters, nil
}

// cluster reads the Cluster this program operates.
func (r *fleetReader) cluster(name string) (*cluster.Cluster, error) {
	return watch.ReadOne[cluster.Cluster](r.client,
		informer.Held{View: r.current(r.clusterCopy), Versions: r.clusterVersions}, name, clusterPath(name))
}

// heartbeats reads every machine's last renewal.
func (r *fleetReader) heartbeats() (map[string]time.Time, error) {
	if leases, ok := watch.List[kubernetes.Lease](r.current(r.leaseCopy)); ok {
		return kubernetes.Renewals(leases), nil
	}
	return kubernetes.ListHeartbeats(r.client)
}

// workloads reads one kind of workload in liken-system for the feature
// janitor. The DaemonSets come from their copy, which the steward
// reads too.
func (r *fleetReader) workloads(listPath string) ([]featureWorkload, error) {
	if listPath == daemonSetsPath {
		if daemonSets, ok := watch.List[featureWorkload](r.current(r.daemonSetCopy)); ok {
			return daemonSets, nil
		}
	}
	return kubernetes.List[featureWorkload](r.client, listPath)
}

// daemonSet reads one DaemonSet in liken-system.
func (r *fleetReader) daemonSet(name string) (*featureWorkload, error) {
	if ds, found, ok := watch.Get[featureWorkload](r.current(r.daemonSetCopy), "liken-system/"+name); ok {
		if !found {
			return nil, apiclient.ErrNotFound
		}
		return ds, nil
	}
	return apiclient.Get[featureWorkload](r.client, daemonSetsPath+"/"+name)
}

// daemonSetPods reads the pods of one stewarded DaemonSet.
func (r *fleetReader) daemonSetPods(name string) ([]kubernetes.Pod, error) {
	if pods, ok := watch.ByIndex[kubernetes.Pod](r.current(r.podCopy), appLabel, name); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, daemonSetPodsPath(name))
}
