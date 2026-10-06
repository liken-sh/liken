package main

// The machine operator's watches, and the reads a pass makes through
// them.
//
// A pass judges six API objects: this machine's Machine, its Node, the
// Cluster, the registry credentials Secret, this node's own operator
// pod, and this node's ResourceSlice. The pass runs at least every ten
// seconds, because the ticker is the heartbeat's clock and the
// backstop for the kernel and file state that sends no event. A pass
// that read each object from the API server would send six requests
// every ten seconds from every machine, and all but the heartbeat's
// would find nothing new. So the operator watches each object, keeps a
// copy in memory, and the pass reads the copies.
//
// Each watch is scoped to the one object, or the few objects, this
// machine reads, by field selector and label selector, so no other
// machine's writes reach this pod. A five-hundred-machine fleet must
// not cost every machine five hundred wakes.
//
// A watch also decides whether a change wakes the loop at once:
//
//   - The Machine wakes the loop on every change, because the
//     conductor's grant and the sweep's Lost verdict are status writes
//     the pass must act on, and they arrive among this operator's own.
//   - The Cluster wakes the loop only on an edit to its spec. The
//     cluster operator writes the Cluster's status after every change
//     in the fleet, and none of that concerns one machine.
//   - The Node, the Secret, and the operator pod wake the loop on every
//     change. The kubelet writes the Node's status only when a
//     condition changes, and the other two change rarely. A cordon, a
//     label, or a taint that somebody set by hand is reverted at once.
//   - The ResourceSlice wakes nothing. This operator is its only
//     writer, and the ticker pass already walks sysfs to compute the
//     inventory it compares.
//
// Every watch wakes the loop once when its first read is done. A pass
// that ran before then read the API server, and a change made between
// that read and the watch's first read is in no event.
//
// A copy that cannot answer, because its watch has not synced yet, is
// never read as the truth. The pass reads the API server instead, the
// way it did before the watch existed. That covers the first seconds
// of a start, and it covers a release skew: right after an upgrade,
// this binary can run under the previous release's RBAC, which grants
// no list or watch on some of these kinds. The watch then keeps
// retrying with a backoff, and every pass reads the API server until
// the new RBAC lands.
//
// A copy also stops answering after any watch that fails, such as each
// watch while k3s restarts (informer.Options.UnreadyOnWatchError), until
// the API server accepts a watch again. The reflector waits out a
// backoff of up to a minute before that, and a write made in that time,
// such as a withdrawn Cluster rollback or a removed reboot approval, is
// in no copy. A pass that acted on the copy could stage the old target
// and write the reboot intent before any 409 stopped it. So the pass
// reads the API server, which fails while it is down: the Node read
// then fails and a granted reboot skips the drain, and the Machine read
// keeps the last copy for the status the pass publishes.

import (
	"context"
	"strings"

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
	likenVersion     = schema.GroupVersion{Group: "liken.sh", Version: strings.TrimPrefix(api.APIVersion, "liken.sh/")}
	machineResource  = likenVersion.WithResource("machines")
	clusterResource  = likenVersion.WithResource("clusters")
	nodeResource     = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	secretResource   = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	podResource      = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	sliceResource    = schema.GroupVersionResource{Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices"}
	credentialsKey   = "liken-system/" + kubernetes.RegistryCredentialsSecret
	operatorPodLabel = "app=liken-machine-operator"
)

// The kind labels of liken_watch_restarts_total, one for each watch.
const (
	nodeKind          = "Node"
	clusterWatchKind  = "Cluster"
	secretKind        = "Secret"
	podKind           = "Pod"
	resourceSliceKind = "ResourceSlice"
)

// watchKinds lists every kind label above, with the Machine's, so the
// counter reports a zero for each watch before its first restart.
var watchKinds = []string{machineKind, nodeKind, clusterWatchKind, secretKind, podKind, resourceSliceKind}

// reader reads each API object a pass judges: from the copy a watch
// keeps, or from the API server when the copy cannot answer. A reader
// with no copies at all reads the API server every time, which is what
// the tests of a pass use.
type reader struct {
	client *apiclient.Client

	// recorder posts the Events of a pass about its Machine (events.go).
	// It is here because every part of a pass that acts reads through
	// the reader. A nil recorder posts nothing.
	recorder *events.Recorder

	machines    *informer.Collection
	nodes       *informer.Collection
	clusters    *informer.Collection
	credentials *informer.Collection
	ownPods     *informer.Collection
	slices      *informer.Collection

	// machineVersions is the memo of this operator's own status writes
	// to its Machine, and of its reads of it from the API server
	// (kubernetes/memo). The watch delivers a write a moment after the
	// API server answers it, so without the memo the next pass could
	// start from a copy older than the status it just wrote. The pass
	// also writes its Node's taints and its ResourceSlice with the
	// version it read, and a copy older than its own write ends in a
	// 409 that the next pass repairs, so neither kind has a memo. Its
	// other Node writes are merge patches that a second pass sends
	// again with no harm.
	machineVersions *memo.Versions
}

// watchThisMachine opens the watches and returns the reader over their
// copies. Each change that needs a pass calls wake. clusterName is
// empty on a machine with no cluster document, which reads no Cluster
// and no Secret, so it opens neither watch.
func watchThisMachine(ctx context.Context, watcher dynamic.Interface, client *apiclient.Client,
	name, clusterName string, wake func(), restarted func(kind string)) *reader {
	start := func(kind string, source informer.Source, handler cache.ResourceEventHandler) *informer.Collection {
		return informer.Start(ctx, watcher, source, informer.Options{
			Handler:  handler,
			Synced:   wake,
			Reopened: func() { restarted(kind) },
			// A copy stops answering after any failed watch, as the
			// head of this file says.
			UnreadyOnWatchError: true,
		})
	}
	named := func(n string) string { return "metadata.name=" + n }

	machines := informer.Source{Resource: machineResource, FieldSelector: named(name)}
	nodes := informer.Source{Resource: nodeResource, FieldSelector: named(name)}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: operatorPodLabel, FieldSelector: "spec.nodeName=" + name}
	slices := informer.Source{Resource: sliceResource, FieldSelector: named(kubernetes.ResourceSliceName(name))}

	r := &reader{client: client, machineVersions: memo.New()}
	r.machines = start(machineKind, machines, watch.WakeOnChange[machine.Machine](machines, wake))
	r.nodes = start(nodeKind, nodes, watch.WakeOnChange[nodeObject](nodes, wake))
	r.ownPods = start(podKind, pods, watch.WakeOnChange[kubernetes.Pod](pods, wake))
	r.slices = start(resourceSliceKind, slices, nil)
	if clusterName != "" {
		clusters := informer.Source{Resource: clusterResource, FieldSelector: named(clusterName)}
		secrets := informer.Source{Resource: secretResource, Namespace: "liken-system",
			FieldSelector: named(kubernetes.RegistryCredentialsSecret)}
		r.clusters = start(clusterWatchKind, clusters, watch.WakeOnEdit[cluster.Cluster](clusters, wake))
		r.credentials = start(secretKind, secrets, watch.WakeOnChange[kubernetes.Secret](secrets, wake))
	}
	return r
}

// machine reads this machine's own Machine. The copy answers only at
// the version of this operator's own last status write or read, and
// the pass otherwise reads the API server once, so a pass never starts
// from a copy older than the status it just wrote.
func (r *reader) machine(name string) (*machine.Machine, error) {
	return watch.ReadOne[machine.Machine](r.client,
		informer.Held{View: r.machines.View(), Versions: r.machineVersions}, name, kubernetes.MachinesPath+"/"+name)
}

// freshMachine reads this machine's Machine from the API server, and
// notes the version for the Machine's copy.
func (r *reader) freshMachine(name string) (*machine.Machine, error) {
	return informer.ReadFresh[machine.Machine](r.client, r.machineVersions, name, kubernetes.MachinesPath+"/"+name)
}

// publishStatus writes this machine's status, and notes the version the
// API server answered for the Machine's copy. A write that fails notes
// that this operator holds no current copy, because a request that
// timed out can still have landed, so the next read goes to the API
// server.
func (r *reader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	return r.machineVersions.Send(m.Metadata.Name, func() (string, error) {
		return kubernetes.PublishStatus(r.client, m, status)
	})
}

// node reads this machine's Node.
func (r *reader) node(name string) (*nodeObject, error) {
	if n, found, ok := watch.Get[nodeObject](r.nodes.View(), name); ok {
		return orNotFound(n, found)
	}
	return getNode(r.client, name)
}

// cluster reads the Cluster this machine belongs to.
func (r *reader) cluster(name string) (*cluster.Cluster, error) {
	if c, found, ok := watch.Get[cluster.Cluster](r.clusters.View(), name); ok {
		return orNotFound(c, found)
	}
	return kubernetes.GetCluster(r.client, name)
}

// registryCredentials reads the fleet's registry credentials. An
// absent Secret returns nil, nil, as GetRegistryCredentialsSecret
// does.
func (r *reader) registryCredentials() (*kubernetes.Secret, error) {
	if s, found, ok := watch.Get[kubernetes.Secret](r.credentials.View(), credentialsKey); ok {
		if !found {
			return nil, nil
		}
		return s, nil
	}
	return kubernetes.GetRegistryCredentialsSecret(r.client)
}

// operatorPods reads this node's own machine-operator pod, as a list
// of one, or none early in a boot.
func (r *reader) operatorPods(nodeName string) ([]kubernetes.Pod, error) {
	if pods, ok := watch.List[kubernetes.Pod](r.ownPods.View()); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, ownPodPath(nodeName))
}

// resourceSlice reads this node's ResourceSlice, nil when it does not
// exist.
func (r *reader) resourceSlice(nodeName string) (*kubernetes.ResourceSlice, error) {
	if s, found, ok := watch.Get[kubernetes.ResourceSlice](r.slices.View(), kubernetes.ResourceSliceName(nodeName)); ok {
		if !found {
			return nil, nil
		}
		return s, nil
	}
	return kubernetes.GetResourceSlice(r.client, nodeName)
}

// orNotFound turns a ready store's answer into the answer a direct read
// gives: the object, or apiclient.ErrNotFound.
func orNotFound[T any](item *T, found bool) (*T, error) {
	if !found {
		return nil, apiclient.ErrNotFound
	}
	return item, nil
}
