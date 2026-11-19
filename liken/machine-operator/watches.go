package main

// The operator watches each API object that a pass reads and keeps a
// copy of it in memory. A pass reads the copies, and most passes start
// because a copy changed. Without the watches, every pass would send a
// request for this machine's Machine, its Node, the Cluster, the
// registry credentials Secret, this node's operator pod, this node's
// ResourceSlice, and its Disruptions, and most of those requests would
// return nothing new.
//
// Each watch uses a field selector or a label selector to receive only
// the objects of this machine. An unselected watch would wake every
// machine in the fleet for each write to another machine's objects.
//
// Each watch also decides which changes start a pass:
//
//   - Every Machine change except the echo of this operator's own
//     status write. cluster-operator's reboot grant and the Lost
//     phase that its sweep writes are status writes too, and the pass
//     must act on them.
//   - A Cluster change only when its spec changed. The cluster
//     operator rewrites the Cluster's status after each change in the
//     fleet.
//   - A Node change unless only the heartbeat times of its conditions
//     changed (withoutHeartbeats). The kubelet rewrites them every five
//     minutes. A pass for each of those writes would keep resetting the
//     backstop timer (backstop.go), so the backstop pass would never
//     run. A person's change to a label or a taint starts a pass, so
//     the pass restores spec.nodeLabels and spec.nodeTaints at once.
//     The pass leaves alone a label, a taint, or a cordon that the
//     operator did not set (labels.go, taints.go, drain.go).
//   - Every change to the Secret and to the operator pod. Both change
//     rarely.
//   - A Disruption that is created, deleted, or whose spec changed.
//     cluster-operator's status writes start no pass.
//   - A ResourceSlice that is deleted, or updated by another writer
//     (sliceHandler). The kubelet deletes a driver's slices when it
//     starts, so a k3s restart deletes this node's slice, and the pass
//     writes it again.
//
// Some waits also watch the node's pods, the PodDisruptionBudgets, the
// HelmCharts, or the LoadBalancer Services, and only while they wait
// (waits.go).
//
// Each watch starts a pass when its first list finishes. A pass that
// ran before then read the API server, and a change made between that
// read and the list arrives in no watch event.
//
// A copy is used only while its watch is synced, and the pass reads the
// API server otherwise. This covers the first seconds after a start.
// It also covers a release upgrade, when this binary can run under the
// previous release's RBAC, which may not grant list or watch on some of
// these kinds. The watch retries with a backoff until the new RBAC is
// applied.
//
// With informer.Options.UnreadyOnWatchError, a copy is also not used
// after its watch fails, for example while k3s restarts, until the API
// server accepts a watch again. The reflector backs off between
// attempts, and the copy misses each write made in that time, such as
// a withdrawn Cluster rollback or a removed reboot approval. A pass
// that read the stale copy could stage the old target and write the
// reboot intent before a 409 stopped it. So the pass reads the API
// server, and while the API server is down those reads fail. A failed
// Machine read makes the pass use its last copy of the Machine
// (loop.go). A failed Node read makes a granted reboot skip the drain,
// as during a demotion (disruptions.go).
//
// A watch that the API server accepts again reports synced before the
// events that it missed arrive. So the next pass reads every kind from
// the API server (reader.throughAPIOnce). The recovery also starts a
// pass, because a pass whose writes failed during the outage
// scheduled nothing else to run it again.

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

var (
	likenVersion       = schema.GroupVersion{Group: "liken.sh", Version: strings.TrimPrefix(api.APIVersion, "liken.sh/")}
	machineResource    = likenVersion.WithResource("machines")
	clusterResource    = likenVersion.WithResource("clusters")
	nodeResource       = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	secretResource     = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	podResource        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	sliceResource      = schema.GroupVersionResource{Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices"}
	disruptionResource = likenVersion.WithResource("disruptions")
	credentialsKey     = "liken-system/" + kubernetes.RegistryCredentialsSecret
	operatorPodLabel   = "app=liken-machine-operator"
)

// The values of the kind label of liken_watch_restarts_total.
const (
	nodeKind          = "Node"
	clusterWatchKind  = "Cluster"
	secretKind        = "Secret"
	podKind           = "Pod"
	resourceSliceKind = "ResourceSlice"
	disruptionKind    = "Disruption"
)

// watchKinds lists every kind label, including the Machine's, so the
// counter reports 0 for each watch before its first restart.
var watchKinds = []string{machineKind, nodeKind, clusterWatchKind, secretKind, podKind, resourceSliceKind, disruptionKind}

// reader reads each API object that a pass uses, from the watch's copy
// when the copy is synced, and from the API server otherwise. A reader
// with no copies, as in the tests of a pass, reads the API server every
// time.
type reader struct {
	client *apiclient.Client

	// recorder posts the Events of a pass about its Machine (events.go).
	// It is a field of reader because every step of a pass that acts
	// receives the reader. A nil recorder posts nothing.
	recorder *events.Recorder

	machines    *informer.Collection
	nodes       *informer.Collection
	clusters    *informer.Collection
	credentials *informer.Collection
	ownPods     *informer.Collection
	slices      *informer.Collection
	disruptions *informer.Collection

	// machineVersions records the resourceVersion of each status write
	// this operator makes to its Machine, and of each read of it from
	// the API server (kubernetes/memo). The watch receives a write a
	// moment after the API server responds to it, so without the
	// record the next pass could read a copy older than the status it
	// just wrote.
	//
	// The Node and the ResourceSlice need no such record. The pass
	// writes the Node's taints and the ResourceSlice with the
	// resourceVersion it read, so a write from a stale copy fails with
	// 409 and the next pass retries. Its other Node writes are merge
	// patches, which are safe to send again.
	machineVersions *memo.Versions

	// statusWritten records this operator's last status write to its
	// Machine (kubernetes.OwnWrite), so the Machine watch starts no
	// pass for the echo of that write.
	statusWritten *kubernetes.OwnWrite

	// slicesWritten records this operator's last write of its
	// ResourceSlice (kubernetes.SliceWriter), so the slice watch starts
	// no pass for the echo of that write.
	slicesWritten *kubernetes.SliceWriter

	// recovered is set when the API server accepts a watch again after
	// a watch of the same kind failed, and throughAPIOnce clears it.
	recovered *atomic.Bool

	// throughAPI makes every read of this pass go to the API server.
	throughAPI bool

	// sysctls records the value that the operator last wrote to each
	// kernel parameter, and the value it read back (conditions.go).
	sysctls *sysctlMemory

	// waits runs the watches that the waits use (waits.go). A nil value
	// starts no watch, and those reads go to the API server.
	waits *waits
}

// withoutHeartbeats removes lastHeartbeatTime from each of a Node's
// conditions, so a kubelet status report that changed no condition
// compares equal to the previous copy.
func withoutHeartbeats(fields map[string]any) {
	conditions, _, _ := unstructured.NestedSlice(fields, "status", "conditions")
	for _, c := range conditions {
		if condition, ok := c.(map[string]any); ok {
			delete(condition, "lastHeartbeatTime")
		}
	}
	if conditions != nil {
		_ = unstructured.SetNestedSlice(fields, conditions, "status", "conditions")
	}
}

// within returns a reader whose requests stop when ctx ends.
func (r *reader) within(ctx context.Context) *reader {
	pass := *r
	pass.client = kubernetes.Within(r.client, ctx)
	return &pass
}

// observedBy returns a reader whose client reports the final result of
// each request to out. It shares every copy, version record, and
// recorder with r. A nil out returns r.
func (r *reader) observedBy(out *passOutcome) *reader {
	if out == nil {
		return r
	}
	pass := *r
	pass.client = r.client.WithObserver(out.observe)
	return &pass
}

// watchThisMachine opens the watches and returns the reader of their
// copies. A change that needs a pass calls wake. clusterName is empty
// on a machine with no cluster document, which reads no Cluster or
// Secret, so it opens neither watch.
func watchThisMachine(ctx context.Context, watcher dynamic.Interface, client *apiclient.Client,
	name, clusterName string, wake func(), restarted func(kind string)) *reader {
	r := &reader{client: client, machineVersions: memo.New(),
		statusWritten: &kubernetes.OwnWrite{}, slicesWritten: &kubernetes.SliceWriter{}, recovered: &atomic.Bool{},
		waits: newWaits(ctx, watcher, wake, name), sysctls: newSysctlMemory()}
	start := func(kind string, source informer.Source, handler cache.ResourceEventHandler) *informer.Collection {
		options := informer.Options{
			Handler:  handler,
			Synced:   wake,
			Reopened: func() { restarted(kind) },
			Recovered: func() {
				r.recovered.Store(true)
				wake()
			},
			// See the comment at the top of this file.
			UnreadyOnWatchError: true,
		}
		// When the cluster's leaders run a release that has no
		// Disruption CRD, the list returns 404. The copy is then empty
		// and reports synced (requests.go).
		if kind == disruptionKind {
			options.Absent = apierrors.IsNotFound
		}
		return informer.Start(ctx, watcher, source, options)
	}
	named := func(n string) string { return "metadata.name=" + n }

	machines := informer.Source{Resource: machineResource, FieldSelector: named(name)}
	nodes := informer.Source{Resource: nodeResource, FieldSelector: named(name)}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: operatorPodLabel, FieldSelector: "spec.nodeName=" + name}
	slices := informer.Source{Resource: sliceResource, FieldSelector: named(kubernetes.ResourceSliceName(name))}

	r.machines = start(machineKind, machines, watch.WakeOnAnotherWritersChange[machine.Machine](machines, wake, r.statusWritten.Wrote))
	r.nodes = start(nodeKind, nodes, watch.WakeOnContent[nodeObject](nodes, wake, withoutHeartbeats))
	r.ownPods = start(podKind, pods, watch.WakeOnChange[kubernetes.Pod](pods, wake))
	r.slices = start(resourceSliceKind, slices, sliceHandler(r.slicesWritten, wake))
	requests := requestsSource(name)
	r.disruptions = start(disruptionKind, requests, watch.WakeOnEdit[disruption.Disruption](requests, wake))
	if clusterName != "" {
		clusters := informer.Source{Resource: clusterResource, FieldSelector: named(clusterName)}
		secrets := informer.Source{Resource: secretResource, Namespace: "liken-system",
			FieldSelector: named(kubernetes.RegistryCredentialsSecret)}
		r.clusters = start(clusterWatchKind, clusters, watch.WakeOnEdit[cluster.Cluster](clusters, wake))
		r.credentials = start(secretKind, secrets, watch.WakeOnChange[kubernetes.Secret](secrets, wake))
	}
	return r
}

// sliceHandler wakes the loop when the slice is deleted or updated by a
// writer other than this operator. The echo of the operator's own write
// is an update at the resourceVersion that the write returned. An
// update with an unchanged resourceVersion, which the informer sends
// after it lists again, is not a change.
func sliceHandler(written *kubernetes.SliceWriter, wake func()) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(before, after any) {
			old, okOld := before.(*unstructured.Unstructured)
			now, okNew := after.(*unstructured.Unstructured)
			if !okOld || !okNew || old.GetResourceVersion() == now.GetResourceVersion() || written.Wrote(now.GetResourceVersion()) {
				return
			}
			wake()
		},
		DeleteFunc: func(any) { wake() },
	}
}

// throughAPIOnce returns a reader that reads every kind from the API
// server when a watch has recovered since the last call, and r
// otherwise. recovered is set before the recovery wakes the loop, so
// the pass that clears it always starts after the recovery.
func (r *reader) throughAPIOnce() *reader {
	if r.recovered == nil || !r.recovered.Swap(false) {
		return r
	}
	pass := *r
	pass.throughAPI = true
	return &pass
}

// machine reads this machine's Machine. It uses the copy only when the
// copy is at the version of this operator's last status write or read
// (machineVersions), and reads the API server otherwise.
func (r *reader) machine(name string) (*machine.Machine, error) {
	if r.throughAPI {
		return r.freshMachine(name)
	}
	return informer.ReadSelected[machine.Machine](r.client,
		informer.Held{View: r.machines.View(), Versions: r.machineVersions}, name, kubernetes.MachinesPath+"/"+name)
}

// freshMachine reads this machine's Machine from the API server and
// records the version it read.
func (r *reader) freshMachine(name string) (*machine.Machine, error) {
	return informer.ReadFresh[machine.Machine](r.client, r.machineVersions, name, kubernetes.MachinesPath+"/"+name)
}

// publishStatus writes this machine's status and records the version
// that the API server returned, and records the write as this
// operator's own. When the write fails, the record marks the copy as
// possibly stale and the next read goes to the API server, because a
// request that timed out can still have been applied.
func (r *reader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	return r.ownStatusWrites().Send(func() (string, error) {
		var version string
		err := r.machineVersions.Send(m.Metadata.Name, func() (string, error) {
			written, err := kubernetes.PublishStatus(r.client, m, status)
			version = written
			return written, err
		})
		return version, err
	})
}

// ownStatusWrites returns the record of this reader's status writes. A
// reader with no watches gets an empty record that nothing consults.
func (r *reader) ownStatusWrites() *kubernetes.OwnWrite {
	if r.statusWritten == nil {
		return &kubernetes.OwnWrite{}
	}
	return r.statusWritten
}

func (r *reader) node(name string) (*nodeObject, error) {
	if n, found, ok := watch.Get[nodeObject](r.view(r.nodes), name); ok {
		return orNotFound(n, found)
	}
	return getNode(r.client, name)
}

func (r *reader) cluster(name string) (*cluster.Cluster, error) {
	if c, found, ok := watch.Get[cluster.Cluster](r.view(r.clusters), name); ok {
		return orNotFound(c, found)
	}
	return kubernetes.GetCluster(r.client, name)
}

// registryCredentials reads the fleet's registry credentials. An
// absent Secret returns nil, nil, as GetRegistryCredentialsSecret
// does.
func (r *reader) registryCredentials() (*kubernetes.Secret, error) {
	if s, found, ok := watch.Get[kubernetes.Secret](r.view(r.credentials), credentialsKey); ok {
		if !found {
			return nil, nil
		}
		return s, nil
	}
	return kubernetes.GetRegistryCredentialsSecret(r.client)
}

// operatorPods lists this node's machine-operator pod. The list is
// empty early in a boot, before the pod exists.
func (r *reader) operatorPods(nodeName string) ([]kubernetes.Pod, error) {
	if pods, ok := watch.List[kubernetes.Pod](r.view(r.ownPods)); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, ownPodPath(nodeName))
}

// resourceSlice reads this node's ResourceSlice, and returns nil when
// it does not exist.
func (r *reader) resourceSlice(nodeName string) (*kubernetes.ResourceSlice, error) {
	if s, found, ok := watch.Get[kubernetes.ResourceSlice](r.view(r.slices), kubernetes.ResourceSliceName(nodeName)); ok {
		if !found {
			return nil, nil
		}
		return s, nil
	}
	return kubernetes.GetResourceSlice(r.client, nodeName)
}

// view returns the view of a copy. When throughAPI is set it returns
// the view of a nil collection, which is never ready, so the read goes
// to the API server.
func (r *reader) view(c *informer.Collection) informer.View {
	if r.throughAPI {
		var none *informer.Collection
		return none.View()
	}
	return c.View()
}

// orNotFound converts the result of a store read into the result of a
// direct read: the object, or apiclient.ErrNotFound.
func orNotFound[T any](item *T, found bool) (*T, error) {
	if !found {
		return nil, apiclient.ErrNotFound
	}
	return item, nil
}

// sliceWriter returns the record of this reader's ResourceSlice writes.
// A reader with no watches gets an empty record that nothing consults.
func (r *reader) sliceWriter() *kubernetes.SliceWriter {
	if r.slicesWritten == nil {
		return &kubernetes.SliceWriter{}
	}
	return r.slicesWritten
}
