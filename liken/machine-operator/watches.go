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

import (
	"context"
	"errors"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/informer"
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

	machines    *informer.Collection
	nodes       *informer.Collection
	clusters    *informer.Collection
	credentials *informer.Collection
	ownPods     *informer.Collection
	slices      *informer.Collection
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
		})
	}
	named := func(n string) string { return "metadata.name=" + n }

	machines := informer.Source{Resource: machineResource, FieldSelector: named(name)}
	nodes := informer.Source{Resource: nodeResource, FieldSelector: named(name)}
	pods := informer.Source{Resource: podResource, Namespace: "liken-system",
		LabelSelector: operatorPodLabel, FieldSelector: "spec.nodeName=" + name}
	slices := informer.Source{Resource: sliceResource, FieldSelector: named(kubernetes.ResourceSliceName(name))}

	r := &reader{client: client}
	r.machines = start(machineKind, machines, informer.WakeOnChange[machine.Machine](machines, wake))
	r.nodes = start(nodeKind, nodes, informer.WakeOnChange[nodeObject](nodes, wake))
	r.ownPods = start(podKind, pods, informer.WakeOnChange[kubernetes.Pod](pods, wake))
	r.slices = start(resourceSliceKind, slices, nil)
	if clusterName != "" {
		clusters := informer.Source{Resource: clusterResource, FieldSelector: named(clusterName)}
		secrets := informer.Source{Resource: secretResource, Namespace: "liken-system",
			FieldSelector: named(kubernetes.RegistryCredentialsSecret)}
		r.clusters = start(clusterWatchKind, clusters, informer.WakeOnEdit[cluster.Cluster](clusters, wake))
		r.credentials = start(secretKind, secrets, informer.WakeOnChange[kubernetes.Secret](secrets, wake))
	}
	return r
}

// machine reads this machine's own Machine. The copy cannot answer
// while it lacks this operator's own last status write, and the pass
// then reads the API server, so a pass never starts from a copy older
// than the status it just wrote (kubernetes/informer's writes.go).
func (r *reader) machine(name string) (*machine.Machine, error) {
	if m, found, ok := informer.Get[machine.Machine](r.machines, name); ok {
		return orNotFound(m, found)
	}
	m, err := kubernetes.GetMachine(r.client, name)
	if err == nil {
		r.machines.Observed(name, m.Metadata.ResourceVersion)
	}
	return m, err
}

// publishStatus writes this machine's status, and records the write
// for the Machine's copy. A conflict wrote nothing. Any other failure
// leaves the outcome unknown, because a request that timed out can
// still have landed.
func (r *reader) publishStatus(m *machine.Machine, status *machine.MachineStatus) error {
	version, err := kubernetes.PublishStatus(r.client, m, status)
	switch {
	case err == nil:
		r.machines.Wrote(m.Metadata.Name, version)
	case !errors.Is(err, apiclient.ErrConflict):
		r.machines.Wrote(m.Metadata.Name, "")
	}
	return err
}

// node reads this machine's Node.
func (r *reader) node(name string) (*nodeObject, error) {
	if n, found, ok := informer.Get[nodeObject](r.nodes, name); ok {
		return orNotFound(n, found)
	}
	return getNode(r.client, name)
}

// cluster reads the Cluster this machine belongs to.
func (r *reader) cluster(name string) (*cluster.Cluster, error) {
	if c, found, ok := informer.Get[cluster.Cluster](r.clusters, name); ok {
		return orNotFound(c, found)
	}
	return kubernetes.GetCluster(r.client, name)
}

// registryCredentials reads the fleet's registry credentials. An
// absent Secret returns nil, nil, as GetRegistryCredentialsSecret
// does.
func (r *reader) registryCredentials() (*kubernetes.Secret, error) {
	if s, found, ok := informer.Get[kubernetes.Secret](r.credentials, credentialsKey); ok {
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
	if pods, ok := informer.List[kubernetes.Pod](r.ownPods); ok {
		return pods, nil
	}
	return kubernetes.List[kubernetes.Pod](r.client, ownPodPath(nodeName))
}

// resourceSlice reads this node's ResourceSlice, nil when it does not
// exist.
func (r *reader) resourceSlice(nodeName string) (*kubernetes.ResourceSlice, error) {
	if s, found, ok := informer.Get[kubernetes.ResourceSlice](r.slices, kubernetes.ResourceSliceName(nodeName)); ok {
		if !found {
			return nil, nil
		}
		return s, nil
	}
	return kubernetes.GetResourceSlice(r.client, nodeName)
}

// orNotFound turns a synced copy's answer into the answer a direct read
// gives: the object, or apiclient.ErrNotFound.
func orNotFound[T any](item *T, found bool) (*T, error) {
	if !found {
		return nil, apiclient.ErrNotFound
	}
	return item, nil
}
