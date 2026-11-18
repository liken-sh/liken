package main

// A demand asks the node to pull a volume now. The webhook, or a person
// with kubectl, writes it as an annotation on the volume's
// PersistentVolume, and every node reads it from a watch of
// PersistentVolumes.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// demandAnnotation holds the time of the demand in RFC 3339.
const demandAnnotation = "git.liken.sh/pull-requested-at"

// demandSkew allows for the clock of the machine that wrote the demand.
// A fetch satisfies a demand when it starts after the demand was
// written, because the fetch then gets every commit that the writer
// asked for. The node compares the demand's time with its own clock, so
// it treats a fetch that starts at time F as satisfying only the demands
// stamped at or before F minus demandSkew. A demand stamped later pulls
// again, so a writer whose clock is behind by less than demandSkew
// still gets its pull. The cost is an extra pull for a demand that a
// fetch already satisfied. A missed demand leaves the volume on an old
// commit until the next demand, so the skew is much wider than the
// clock drift between machines that run NTP.
const demandSkew = time.Minute

// persistentVolumeKind is the kind label of the
// git_csi_watch_restarts_total metric for the PersistentVolume watch.
const persistentVolumeKind = "PersistentVolume"

// defaultDemandMin is the default --demand-min-interval: how long a
// demand waits after the last pass of the repository's loop before it
// pulls.
const defaultDemandMin = 10 * time.Second

// demandWatch watches every PersistentVolume of the cluster for demands.
// The node opens one watch for all of its volumes, so it holds one
// connection to the API server however many volumes it stages.
type demandWatch struct {
	node   *node
	client kubernetes.Interface
	logger *slog.Logger

	// seen is the last demand read for each volume handle, including
	// handles that this node has not staged yet, because a stage that
	// ends after the demand arrived reads it from here.
	mu   sync.Mutex
	seen map[string]time.Time

	// store is the informer's cache of PersistentVolumes, indexed by
	// handleIndex. It is nil while no watch runs, which includes a
	// driver outside a cluster.
	store atomic.Pointer[cache.Indexer]
	// listed is true from the informer's first sync until the watch
	// ends. Before the first sync, an empty store does not mean that a
	// PersistentVolume was deleted.
	listed atomic.Bool
}

// handleIndex indexes the store by volume handle, because the kubelet
// names a volume by its handle and not by its PersistentVolume.
const handleIndex = "handle"

// volumeHandle returns the handle of a PersistentVolume of this driver,
// and no key for any other object.
func volumeHandle(object any) ([]string, error) {
	pv, isVolume := object.(*corev1.PersistentVolume)
	if !isVolume || pv.Spec.CSI == nil || pv.Spec.CSI.Driver != driverName {
		return nil, nil
	}
	return []string{pv.Spec.CSI.VolumeHandle}, nil
}

// cachedVolume returns the cached PersistentVolume with the handle. It
// returns false when there is none, and also when two PersistentVolumes
// of this driver have the same handle, because then neither is the
// right one.
func (d *demandWatch) cachedVolume(handle string) (*corev1.PersistentVolume, bool) {
	store := d.store.Load()
	if store == nil {
		return nil, false
	}
	found, err := (*store).ByIndex(handleIndex, handle)
	if err != nil || len(found) != 1 {
		return nil, false
	}
	pv, isVolume := found[0].(*corev1.PersistentVolume)
	return pv, isVolume
}

// hasVolume reports whether a PersistentVolume of this driver has the
// handle. It returns true until the informer's first sync, because the
// caller deletes the work tree of a handle with no PersistentVolume.
func (d *demandWatch) hasVolume(handle string) bool {
	store := d.store.Load()
	if store == nil || !d.listed.Load() {
		return true
	}
	found, err := (*store).ByIndex(handleIndex, handle)
	return err != nil || len(found) > 0
}

func newDemandWatch(n *node, client kubernetes.Interface, logger *slog.Logger) *demandWatch {
	return &demandWatch{
		node:   n,
		client: client,
		logger: logger,
		seen:   map[string]time.Time{},
	}
}

// run lists and watches PersistentVolumes until ctx ends. A driver
// outside a cluster has no client and reads no demands.
//
// The first list reads the demands written while no watch was open. It
// also shows which work trees lost their PersistentVolume while the
// plugin was down, so reclaimAll runs after the first sync.
func (d *demandWatch) run(ctx context.Context) {
	if d.client == nil {
		return
	}
	volumes := d.client.CoreV1().PersistentVolumes()
	store, informer := collection{
		kind:   persistentVolumeKind,
		client: d.client,
		object: &corev1.PersistentVolume{},
		list: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return volumes.List(ctx, options)
		},
		watch: volumes.Watch,
		handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(object any) {
				d.read(ctx, object.(*corev1.PersistentVolume))
			},
			UpdateFunc: func(_, object any) {
				d.read(ctx, object.(*corev1.PersistentVolume))
			},
			DeleteFunc: func(object any) { d.deleted(ctx, object) },
		},
		metrics:  d.node.metrics,
		indexers: cache.Indexers{handleIndex: volumeHandle},
	}.informer()
	d.store.Store(&store)
	defer d.store.Store(nil)
	first := make(chan struct{})
	go func() {
		defer close(first)
		if cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			d.listed.Store(true)
			d.node.reclaimAll(ctx)
		}
	}()
	informer.RunWithContext(ctx)
	<-first
	d.listed.Store(false)
}

// deleted forgets the demand of a deleted PersistentVolume and reclaims
// the work tree of its handle under the last reclaim policy the node
// recorded. When the watch missed the delete, the informer's next list
// reports it as a tombstone that wraps the last cached copy. The
// informer removes the object from its store before it calls this
// handler, so hasVolume checks only the PersistentVolumes that remain.
func (d *demandWatch) deleted(ctx context.Context, object any) {
	pv, isVolume := unwrapped(object).(*corev1.PersistentVolume)
	if !isVolume {
		// An informer of PersistentVolumes sends no other type, so this
		// is a fault in client-go. Without the PersistentVolume there
		// is no handle to forget, so the handle's last demand stays in
		// seen. Only a later stage of the same handle reads that entry,
		// and the fetch of that stage has already satisfied it.
		d.logger.WarnContext(ctx, "The delete event carried no PersistentVolume",
			"object", fmt.Sprintf("%#v", object))
		return
	}
	d.forget(pv)
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != driverName {
		return
	}
	// While another PersistentVolume has the same handle, the work tree
	// and its recorded reclaim policy stay.
	if d.hasVolume(pv.Spec.CSI.VolumeHandle) {
		return
	}
	d.node.recordReclaimPolicy(ctx, pv)
	d.node.reclaim(ctx, pv.Spec.CSI.VolumeHandle)
}

// read handles one add or update of a PersistentVolume of this driver.
// It records the reclaim policy and saves the demand, and pulls when
// this node staged the handle and no fetch has satisfied the demand.
//
// It saves the demand even for a handle this node has not staged. A
// stage fetches before it adds the volume to the node, and actOnSavedDemand
// reads the saved demand when the stage ends.
//
// The webhook never removes its annotation, so the list, the watch,
// and a restart read the same old demand many times. Only the demand's
// time decides whether to pull, so a repeated read does nothing.
func (d *demandWatch) read(ctx context.Context, pv *corev1.PersistentVolume) {
	source := pv.Spec.CSI
	if source == nil || source.Driver != driverName {
		return
	}
	d.node.recordReclaimPolicy(ctx, pv)
	value := pv.Annotations[demandAnnotation]
	if value == "" {
		return
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		d.logger.WarnContext(ctx, "The pull annotation is not an RFC 3339 time",
			"volume", pv.Name, "value", value, "error", err)
		return
	}
	d.mu.Lock()
	d.seen[source.VolumeHandle] = at
	d.mu.Unlock()
	d.actOn(ctx, source.VolumeHandle, at)
}

// forget deletes the saved demand of a deleted PersistentVolume, so
// seen does not grow for as long as the driver runs.
func (d *demandWatch) forget(pv *corev1.PersistentVolume) {
	if pv.Spec.CSI == nil {
		return
	}
	d.mu.Lock()
	delete(d.seen, pv.Spec.CSI.VolumeHandle)
	d.mu.Unlock()
}

// actOnSavedDemand acts on the saved demand of a volume that a stage just added
// to the node. The watch does not send that demand again, so without
// this call a demand that arrived during the stage would leave the
// volume on its old commit until the next demand.
func (d *demandWatch) actOnSavedDemand(ctx context.Context, handle string) {
	d.mu.Lock()
	at, found := d.seen[handle]
	d.mu.Unlock()
	if found {
		d.actOn(ctx, handle, at)
	}
}

func (d *demandWatch) actOn(ctx context.Context, handle string, at time.Time) {
	staged := d.node.stagedVolume(handle)
	if staged == nil {
		return
	}
	if !staged.acceptDemand(at, time.Now()) {
		return
	}
	if staged.writeable() {
		// Only the application changes a writeable tree. A pull would
		// rewrite the pod's files while it runs.
		d.logger.InfoContext(ctx, "Ignored a pull demand for a writeable volume",
			"volume", staged.id, "reason", "the volume is writeable")
		return
	}
	d.node.demand(staged)
}

// demand passes a demand to the loop of the volume's repository. It
// drops the demand when the volume's pull attribute is "never",
// because such a volume joins no loop.
func (n *node) demand(v *volume) {
	if !v.attributes.pull.follows() {
		return
	}
	n.mu.Lock()
	loop := n.loopOf(v)
	n.mu.Unlock()
	if loop == nil {
		return
	}
	loop.demand(v)
}

// loopOf returns the loop of the volume's repository, or nil. The
// caller holds the node's lock.
func (n *node) loopOf(v *volume) *follower {
	return n.followers[n.store.repository(v.attributes.url).name]
}

// demand adds the volume to the loop's wanted set and wakes the loop.
// The send to the wake channel, which has a buffer of one, does not
// block, so a burst of demands never waits on a loop that is fetching.
//
// A volume that waits for its credential ignores demands. Its fetch
// would fail, and the wake would fetch the repository's other volumes
// for nothing. The pass that runs when the credential is republished
// satisfies the demand.
func (f *follower) demand(v *volume) {
	if v.waitsForCredential() {
		return
	}
	f.mu.Lock()
	if f.volumes[v.id] != v {
		// An unstage removed the volume from the loop after the node
		// looked the loop up.
		f.mu.Unlock()
		return
	}
	if f.wanted[v.id] == nil {
		// git_csi_demanded_pulls_total counts pulls, not demands. More
		// demands before the pass, and the retry of a failed fetch, do
		// not count again.
		f.node.metrics.demanded(v)
	}
	f.wanted[v.id] = v
	f.mu.Unlock()
	v.reportDemanded(time.Now())
	select {
	case f.demanded <- struct{}{}:
	default:
	}
}

// demandWait returns how long a demand must still wait before it
// pulls: zero before the loop's first pass, and after that the rest of
// --demand-min-interval since the last pass.
func (f *follower) demandWait(now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastPull.IsZero() {
		return 0
	}
	return f.node.demandMin - now.Sub(f.lastPull)
}

// startPass records the start of a pass and returns the volumes that
// were demanded since the last pass.
func (f *follower) startPass(at time.Time) map[string]*volume {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := f.wanted
	f.wanted = map[string]*volume{}
	f.lastPull = at
	return wanted
}

// resumePull demands a pull of a volume that a restart resumed. The
// restart treats every demand stamped before it as satisfied, so a demand
// written while the node plugin was down starts no pull, and a volume
// with pull on-demand has no timer to pull it later. The caller holds
// the node's lock.
func (n *node) resumePull(resumed *volume) {
	if !resumed.attributes.pull.follows() {
		return
	}
	if loop := n.loopOf(resumed); loop != nil {
		loop.demand(resumed)
	}
}
