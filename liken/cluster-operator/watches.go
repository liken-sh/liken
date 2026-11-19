package main

// Each pass reads the whole fleet: every Machine, every heartbeat
// Lease, the Cluster, the DaemonSets in liken-system, and the pods of
// each DaemonSet in refreshedDaemonSets. The ticker in main.go runs a
// pass every ten seconds, because only a pass marks a machine Lost
// when its heartbeat ages and reports a rollout whose granted turn has
// stalled. Status writes across the fleet start more passes. Reading
// every collection from the API server on each pass would send one
// request per collection each time. So the operator watches each
// collection, caches it in memory, and the pass reads the caches.
//
// Each watch's handler decides whether a change starts a pass at once:
//
//   - Any change to a Machine starts one, because it can change the
//     Cluster's phase, the disruption budget, or a Lost phase.
//   - The Cluster starts one only on a spec change. This program
//     writes the Cluster's status, and that write must not start
//     another pass.
//   - A DaemonSet starts one only on a spec change. A leader's boot
//     writes a new pod template, and the pass then runs refreshOSPods
//     to evict the pods made from the old template. The DaemonSet
//     controller writes status often, and the pass does not read it.
//   - A Disruption or a MachineDisruptionBudget starts one on a spec
//     change or a deletion: a new or deleted request, an approval, or a
//     new limit. This program writes the status of both. When
//     machine-operator marks a request Complete after the reboot, the
//     Machine's new boot is a Machine change, which starts a pass.
//   - Heartbeat Leases and pods start nothing. The ticker checks each
//     heartbeat's age, and refreshOSPods acts on a machine's version,
//     which arrives as a Machine change. The Lease handler records when
//     each Lease's renewTime changed, on this program's clock, and the pass
//     measures a heartbeat's age from that record (heartbeats.go).
//
// The pass reads a cache only while it has synced and its last watch
// has not failed, and reads the API server otherwise. That covers the
// first seconds after this process becomes leader, a binary that runs
// under the previous release's RBAC, and an API server restart. After a
// failed watch, client-go's reflector waits up to a minute before it
// watches again, and the cache misses every write in that time, such as
// a machine's Degraded status, which the disruption budget must count.
//
// The pass always reads the flux deploy key Secret and the
// flux-system Namespace from the API server. It reads the Secret only
// while the flux feature is declared, and the RBAC that allows the read
// comes with the feature's manifests, so a watch would be refused on
// every fleet without flux. It reads the Namespace only while the
// feature is not declared, to find an installation to remove, and this
// program may read that Namespace only by name.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/watch"
	"github.com/liken-sh/liken/liken/machine"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The resources this operator watches, as the dynamic client names them.
var (
	likenVersion       = schema.GroupVersion{Group: "liken.sh", Version: strings.TrimPrefix(api.APIVersion, "liken.sh/")}
	machineResource    = likenVersion.WithResource("machines")
	clusterResource    = likenVersion.WithResource("clusters")
	leaseResource      = schema.GroupVersionResource{Group: "coordination.k8s.io", Version: "v1", Resource: "leases"}
	daemonSetResource  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	podResource        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	disruptionResource = likenVersion.WithResource("disruptions")
	budgetResource     = likenVersion.WithResource("machinedisruptionbudgets")
)

// The values of the kind label of liken_watch_restarts_total.
const (
	leaseKind      = "Lease"
	daemonSetKind  = "DaemonSet"
	podKind        = "Pod"
	disruptionKind = "Disruption"
	budgetKind     = "MachineDisruptionBudget"
)

// watchKinds lists every kind label, so the counter reports zero for
// each watch before its first restart.
var watchKinds = []string{machineKind, clusterKind, leaseKind, daemonSetKind, podKind, disruptionKind, budgetKind}

// appLabel is the pod label that names a DaemonSet in
// refreshedDaemonSets. The pod
// cache is indexed by it, so reading one DaemonSet's pods does not scan
// every pod.
const appLabel = "app"

// fleetReader reads each API object a pass needs, from the watch's
// cache when the cache is usable and from the API server otherwise. A
// fleetReader with no caches reads the API server every time, which is
// how most pass tests construct it.
type fleetReader struct {
	client *apiclient.Client

	// recorder posts the Events about the Machines and the Cluster
	// (events.go). It is here because every part of a pass that writes
	// already has the fleetReader. A nil recorder posts nothing.
	recorder *events.Recorder

	machineCache    *informer.Collection
	clusterCache    *informer.Collection
	leaseCache      *informer.Collection
	daemonSetCache  *informer.Collection
	podCache        *informer.Collection
	disruptionCache *informer.Collection
	budgetCache     *informer.Collection

	// machineVersions and clusterVersions record the resourceVersion of
	// each Machine and Cluster that this program last wrote or read
	// from the API server (kubernetes/memo). The watch receives a write
	// some time after the API server accepts it. A pass that read a
	// cache without its own last grant would count one fewer machine
	// rebooting, and could grant a turn beyond the disruption budget.
	// So the pass uses a cached object only at the recorded version,
	// and reads that object from the API server otherwise.
	machineVersions *memo.Versions
	clusterVersions *memo.Versions

	// disruptionVersions and budgetVersions do the same for this
	// program's status writes to Disruptions and budgets.
	disruptionVersions *memo.Versions
	budgetVersions     *memo.Versions

	// sightings records when this program saw each heartbeat Lease
	// change (heartbeats.go). The Lease watch's handler writes it.
	sightings heartbeatSightings
}

// watchFleet starts the watches and returns the reader over their
// caches. wake is called for each change that needs a pass, and
// restarted is called with a watch's kind each time its watch reopens.
func watchFleet(ctx context.Context, watcher dynamic.Interface, client *apiclient.Client,
	wake func(), restarted func(kind string)) *fleetReader {
	start := func(kind string, source informer.Source, handler cache.ResourceEventHandler, indexers cache.Indexers) *informer.Collection {
		options := informer.Options{
			Handler:  handler,
			Synced:   wake,
			Reopened: func() { restarted(kind) },
			// A cache misses every write from a failed watch until
			// the reflector watches again, so the pass reads the API
			// server in that time.
			UnreadyOnWatchError: true,
			Indexers:            indexers,
		}
		// A cluster whose leaders run a release from before the
		// Disruption kinds does not serve them. The watch treats a 404
		// as an absent collection until a leader's boot installs the
		// definitions.
		if kind == disruptionKind || kind == budgetKind {
			options.Absent = apierrors.IsNotFound
		}
		return informer.Start(ctx, watcher, source, options)
	}
	machines := informer.Source{Resource: machineResource}
	clusters := informer.Source{Resource: clusterResource}
	leases := informer.Source{Resource: leaseResource, Namespace: "liken-system"}
	daemonSets := informer.Source{Resource: daemonSetResource, Namespace: "liken-system"}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: appLabel + " in (" + strings.Join(refreshedDaemonSets, ",") + ")"}

	r := &fleetReader{
		client:          client,
		machineCache:    start(machineKind, machines, watch.WakeOnChange[machine.Machine](machines, wake), nil),
		clusterCache:    start(clusterKind, clusters, watch.WakeOnEdit[cluster.Cluster](clusters, wake), nil),
		daemonSetCache:  start(daemonSetKind, daemonSets, watch.WakeOnEdit[annotatedObject](daemonSets, wake), nil),
		podCache:        start(podKind, pods, nil, cache.Indexers{appLabel: watch.LabelIndex(appLabel)}),
		machineVersions: memo.New(),
		clusterVersions: memo.New(),
	}
	disruptions := informer.Source{Resource: disruptionResource}
	budgets := informer.Source{Resource: budgetResource}
	r.disruptionCache = start(disruptionKind, disruptions, watch.WakeOnEdit[disruption.Disruption](disruptions, wake), nil)
	r.budgetCache = start(budgetKind, budgets, watch.WakeOnEdit[disruption.Budget](budgets, wake), nil)
	r.disruptionVersions, r.budgetVersions = memo.New(), memo.New()
	r.leaseCache = start(leaseKind, leases, r.sightings.handler(leases), nil)
	return r
}

// current returns the view of a cache that the pass may read, or an
// empty view, which makes the caller read the API server.
//
// All watches of this process share one HTTP/2 connection. When the
// API server at the other end loses power or leaves the network, the
// stream stops sending, and client-go closes it only about 45 seconds
// after the last frame (a 30-second read idle timeout, then a
// 15-second ping timeout). The caches look current the whole time. A
// pass that read heartbeats from them would see every heartbeat age,
// and would mark live machines Lost after HeartbeatStaleAfter, through
// a status write to another API server on a new connection.
//
// So current first reads this program's leader Lease from the Lease
// cache. Leader election renews that Lease every RetryPeriod, and the
// watch receives each renewal. When the cached renewal is
// cacheFreshness old or older, the stream or the renewals have
// stopped, and the pass reads the API server. A cached heartbeat is
// then at most cacheFreshness behind, which is less than
// HeartbeatStaleAfter. The election uses the same connection, so its
// write guard also refuses every write once the last renewal is
// RenewDeadline old, and the process exits when the election gives up.
//
// This check finds a stalled connection. A failed watch is a separate
// case, which informer.Options.UnreadyOnWatchError handles, because
// each reflector recovers on its own backoff.
//
// The view of the Machine, Cluster, Disruption, and budget caches is
// marked Whole. Their watches have no selector, so informer.List also
// reads from the API server each object that this program wrote and
// the cache has not received yet. The other caches are read without a
// version record, so the flag has no effect on them.
func (r *fleetReader) current(held *informer.Collection) informer.View {
	if held == nil {
		return informer.View{}
	}
	lease, found, ok := watch.Get[kubernetes.Lease](r.leaseCache.View(), leaseNamespace+"/"+leaseName)
	if !ok || !found {
		return informer.View{}
	}
	renewed, err := time.Parse(time.RFC3339Nano, lease.Spec.RenewTime)
	if err != nil || time.Since(renewed) >= cacheFreshness {
		return informer.View{}
	}
	view := held.View()
	view.Whole = held == r.machineCache || held == r.clusterCache || held == r.disruptionCache || held == r.budgetCache
	return view
}

// cacheFreshness is the age of the leader Lease's last renewal in the
// Lease cache at which the pass stops reading the caches.
var cacheFreshness = election.RenewDeadline + election.RetryPeriod

// machines reads every Machine in the fleet. It reads a Machine from
// the API server when the cached version differs from the version this
// program last wrote or read, such as a Machine without the grant the
// last pass wrote. While the cache is unusable, it lists every Machine
// from the API server and records each version, so the pass does not
// use a cache that recovers at an older version of that Machine.
func (r *fleetReader) machines() ([]machine.Machine, error) {
	held := informer.Held{View: r.current(r.machineCache), Versions: r.machineVersions}
	machines, err := informer.List[machine.Machine](r.client, held, kubernetes.MachinesPath, machinePath)
	if err != nil {
		return nil, err
	}
	forgetSettledVersions(held, machines)
	return machines, nil
}

// readTurns replaces in machines each Machine that has a turn in the
// Cluster's status.rebootTurns and shows no grant, with a read from the
// API server, and removes a Machine that the API server no longer has.
// decideRollout ends such a turn when the Machine's resourceVersion
// differs from the turn's. A stale cached Machine also differs while
// the grant write can still succeed, so the version must come from the
// API server. A Machine that shows a grant needs no read, because its
// turn counts against the budget either way. When no machine waits for
// a reboot, status.rebootTurns is empty and readTurns sends nothing.
func (r *fleetReader) readTurns(turns []cluster.RebootTurn, machines []machine.Machine) ([]machine.Machine, error) {
	machines = slices.Clone(machines)
	for _, turn := range turns {
		i := slices.IndexFunc(machines, func(m machine.Machine) bool { return m.Metadata.Name == turn.Machine })
		if i >= 0 && api.FindCondition(machines[i].Status.Conditions, machine.RebootApprovedCondition) != nil {
			continue
		}
		fresh, err := memo.ReadFresh[machine.Machine](r.client, r.machineVersions, turn.Machine, machinePath(turn.Machine))
		switch {
		case errors.Is(err, apiclient.ErrNotFound):
			if i >= 0 {
				machines = slices.Delete(machines, i, i+1)
			}
		case err != nil:
			return nil, err
		case i >= 0:
			machines[i] = *fresh
		default:
			machines = append(machines, *fresh)
		}
	}
	return machines, nil
}

func machinePath(name string) string { return kubernetes.MachinesPath + "/" + name }

func clusterPath(name string) string { return kubernetes.ClustersPath + "/" + name }

// forgetSettledVersions removes recorded versions after a list. It removes the
// record of each object that is in neither the list nor the store, such
// as a deleted Machine, which would otherwise stay for the life of the
// process. It also removes the record of each object that the store has
// at the recorded version (informer.ForgetStored), so a later write by
// another writer does not force a read. When the view is not ready, the
// list came from the API server and there is no store to compare with,
// so the records stay.
func forgetSettledVersions[T any, P informer.Object[T]](held informer.Held, items []T) {
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
	informer.ForgetStored(held, keys...)
}

// publishStatus writes a Machine's status and records the version the
// API server returned. After a failed write, the next read of that
// Machine goes to the API server, because a request that timed out can
// still have been applied.
func (r *fleetReader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	return r.machineVersions.Send(m.Metadata.Name, func() (string, error) {
		return kubernetes.PublishStatus(r.client, m, status)
	})
}

// publishClusterStatus writes the Cluster's status and records the
// returned version, so the next pass does not compare its result with
// a status older than its own last write. It returns the Cluster as the
// API server stored it, or nil when the write returned no Cluster.
func (r *fleetReader) publishClusterStatus(clusterDoc *cluster.Cluster) (*cluster.Cluster, error) {
	var stored *cluster.Cluster
	err := r.clusterVersions.Send(clusterDoc.Metadata.Name, func() (string, error) {
		written, err := kubernetes.PublishClusterStatus(r.client, clusterDoc)
		if err != nil || written == nil {
			return "", err
		}
		stored = written
		return written.Metadata.ResourceVersion, nil
	})
	return stored, err
}

// clusters reads every Cluster.
func (r *fleetReader) clusters() ([]cluster.Cluster, error) {
	held := informer.Held{View: r.current(r.clusterCache), Versions: r.clusterVersions}
	clusters, err := informer.List[cluster.Cluster](r.client, held, kubernetes.ClustersPath, clusterPath)
	if err != nil {
		return nil, err
	}
	forgetSettledVersions(held, clusters)
	return clusters, nil
}

func (r *fleetReader) cluster(name string) (*cluster.Cluster, error) {
	return informer.ReadSelected[cluster.Cluster](r.client,
		informer.Held{View: r.current(r.clusterCache), Versions: r.clusterVersions}, name, clusterPath(name))
}

// heartbeats returns, for each machine's heartbeat Lease, when this
// program last saw its renewTime change, by this program's clock
// (heartbeats.go). A change that this read finds before the watch
// handler receives it is recorded at now.
func (r *fleetReader) heartbeats(now time.Time) (map[string]time.Time, error) {
	if leases, ok := watch.List[kubernetes.Lease](r.current(r.leaseCache)); ok {
		return r.sightings.changeTimes(kubernetes.Renewals(leases), now), nil
	}
	renewals, err := kubernetes.ListHeartbeats(r.client)
	if err != nil {
		return nil, err
	}
	return r.sightings.changeTimes(renewals, now), nil
}

// workloads lists one kind of workload in liken-system, for the removal
// of retracted features (janitor.go). DaemonSets come from the
// DaemonSet cache.
func (r *fleetReader) workloads(listPath string) ([]annotatedObject, error) {
	if listPath == daemonSetsPath {
		if daemonSets, ok := watch.List[annotatedObject](r.current(r.daemonSetCache)); ok {
			return daemonSets, nil
		}
	}
	return kubernetes.List[annotatedObject](r.client, listPath)
}

func (r *fleetReader) daemonSet(name string) (*annotatedObject, error) {
	if ds, found, ok := watch.Get[annotatedObject](r.current(r.daemonSetCache), "liken-system/"+name); ok {
		if !found {
			return nil, apiclient.ErrNotFound
		}
		return ds, nil
	}
	return apiclient.Get[annotatedObject](r.client, daemonSetsPath+"/"+name)
}

func (r *fleetReader) daemonSetPods(name string) ([]kubernetes.Pod, error) {
	if pods, ok := watch.ByIndex[kubernetes.Pod](r.current(r.podCache), appLabel, name); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, daemonSetPodsPath(name))
}

// requests reads every Disruption and every MachineDisruptionBudget. On
// a cluster that does not serve a kind, or whose RBAC does not allow it,
// requests returns none of that kind. Any other error fails the read,
// because a pass that missed a budget could grant a turn beyond it.
func (r *fleetReader) requests() (fleetRequests, error) {
	disruptions, _, err := readKind[disruption.Disruption](r, r.disruptionCache, r.disruptionVersions,
		kubernetes.DisruptionsPath, disruptionPath)
	if err != nil {
		return fleetRequests{}, fmt.Errorf("reading the Disruptions: %w", err)
	}
	budgets, served, err := readKind[disruption.Budget](r, r.budgetCache, r.budgetVersions,
		kubernetes.BudgetsPath, budgetPath)
	if err != nil {
		return fleetRequests{}, fmt.Errorf("reading the MachineDisruptionBudgets: %w", err)
	}
	return fleetRequests{disruptions: disruptions, budgets: budgets, budgetsServed: served}, nil
}

// readKind lists Disruptions or budgets through their cache. For a kind
// that the cluster does not serve, or that this program may not read,
// it returns no items and served false.
func readKind[T any, P informer.Object[T]](r *fleetReader, cached *informer.Collection, versions *memo.Versions,
	listPath string, path func(string) string) (items []T, served bool, err error) {
	held := informer.Held{View: r.current(cached), Versions: versions}
	if held.View.Ready() && held.View.Absent {
		return nil, false, nil
	}
	items, err = informer.List[T, P](r.client, held, listPath, path)
	if errors.Is(err, apiclient.ErrNotFound) || errors.Is(err, apiclient.ErrForbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	forgetSettledVersions[T, P](held, items)
	return items, true, nil
}

func disruptionPath(name string) string { return kubernetes.DisruptionsPath + "/" + name }

func budgetPath(name string) string { return kubernetes.BudgetsPath + "/" + name }
