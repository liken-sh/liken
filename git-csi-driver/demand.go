package main

// demand.go is the channel that pulls a read-only volume from outside
// the node: the annotation on the PersistentVolume, the one list and
// watch that read it, and the interval that bounds a burst of demands.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// demandAnnotation is where a demand is written. The value is the time
// of the demand in RFC 3339, the form kubectl describe shows a person,
// and the form the webhook writes.
const demandAnnotation = "git.liken.sh/pull-requested-at"

// demandSkew is how far the writer's clock may run behind this node's
// clock before the node misses a demand.
//
// A demand's time comes from the clock of whatever wrote it: the
// controller's node for a webhook, or a person's computer for kubectl.
// The node compares that time with its own clock. A fetch that starts
// at time F answers every demand stamped at or before F minus the skew,
// and the node pulls for every later one.
//
// A writer whose clock runs behind by less than the skew stamps a new
// demand earlier than it happened. The demand is still later than F
// minus the skew, so the node pulls. A writer whose clock runs ahead
// stamps a demand later than it happened, and the node pulls for it.
// The cost of either error is one extra pull. A demand the node misses
// leaves the volume on an old commit until the next demand, so the
// skew is generous: a minute is far wider than the clock drift between
// machines that keep time with NTP.
const demandSkew = time.Minute

// persistentVolumeKind is what git_csi_watch_restarts_total names the
// node's one watch on PersistentVolumes.
const persistentVolumeKind = "PersistentVolume"

// defaultDemandMin is the default --demand-min-interval. It bounds a
// burst of demands to six pulls a minute per repository per node.
const defaultDemandMin = 10 * time.Second

// demanding is the one watch on PersistentVolumes the node holds. One
// watch covers every volume the node stages, so a node with fifty
// volumes holds one connection to the API server, not fifty.
type demanding struct {
	node   *node
	client kubernetes.Interface
	logger *slog.Logger

	// seen is the last demand the node read, by volume handle, whether
	// or not this node staged the handle. A stage reads it when it adds
	// the volume to the node.
	mu   sync.Mutex
	seen map[string]time.Time
}

func newDemanding(answering *node, client kubernetes.Interface, logger *slog.Logger) *demanding {
	return &demanding{
		node:   answering,
		client: client,
		logger: logger,
		seen:   map[string]time.Time{},
	}
}

// follow holds the list and the watch for the driver's whole run. A
// driver outside a cluster holds no client, so it reads no demand. The
// first read takes every PersistentVolume once, which catches a demand
// written while no watch was open, and the watch carries every demand
// after it.
//
// Every add and every update is read, because a demand is an
// annotation, and an annotation changes no generation. read acts only
// on a demand later than what the volume's last fetch answered, so a
// status write the watch also sends acts on nothing.
func (d *demanding) follow(ctx context.Context) {
	if d.client == nil {
		return
	}
	volumes := d.client.CoreV1().PersistentVolumes()
	collection{
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
		readings: d.node.readings,
	}.follow(ctx)
}

// deleted forgets the demand of a PersistentVolume the watch reports
// deleted. A read after a gap in the watch reports a PersistentVolume
// it no longer holds as a tombstone, which carries the last copy the
// informer held.
func (d *demanding) deleted(ctx context.Context, object any) {
	held, isVolume := unwrapped(object).(*corev1.PersistentVolume)
	if !isVolume {
		// The informer of a typed kind stores only that kind, so this
		// names a fault in client-go. The demand stays in seen, where
		// it costs one entry and acts on nothing new, because a fetch
		// already answered it.
		d.logger.WarnContext(ctx, "the delete carries no PersistentVolume",
			"object", fmt.Sprintf("%#v", object))
		return
	}
	d.forget(held)
}

// read acts on one PersistentVolume. It acts only when the volume is
// this driver's, only when this node staged the handle, and only when
// the demand is later than what the volume's last fetch answered. It
// records the demand for a handle this node has not staged, because a
// stage fetches before it adds the volume to the node, and a demand
// that arrives between the two is read again when the stage ends.
//
// The time decides, not the order of reads. A webhook never removes
// its annotation, so the list, the watch, a stage, and a restart all
// read old demands, in any order, and the volume's own fetches already
// answer them.
func (d *demanding) read(ctx context.Context, held *corev1.PersistentVolume) {
	source := held.Spec.CSI
	if source == nil || source.Driver != driverName {
		return
	}
	asked := held.Annotations[demandAnnotation]
	if asked == "" {
		return
	}
	at, err := time.Parse(time.RFC3339, asked)
	if err != nil {
		d.logger.WarnContext(ctx, "the demand is not a time",
			"volume", held.Name, "value", asked, "error", err)
		return
	}
	d.mu.Lock()
	d.seen[source.VolumeHandle] = at
	d.mu.Unlock()
	d.actOn(ctx, source.VolumeHandle, at)
}

// forget drops the demand the node read for a deleted PersistentVolume,
// so seen holds one entry for each PersistentVolume that exists, not
// for every one the driver ever read.
func (d *demanding) forget(held *corev1.PersistentVolume) {
	if held.Spec.CSI == nil {
		return
	}
	d.mu.Lock()
	delete(d.seen, held.Spec.CSI.VolumeHandle)
	d.mu.Unlock()
}

// arrived acts on the last demand the node read for a volume a stage
// just added to the node. The watch sends no event for that demand
// again, so without this read a demand that arrived during the stage
// leaves the volume on its old commit until the next demand.
func (d *demanding) arrived(ctx context.Context, handle string) {
	d.mu.Lock()
	at, found := d.seen[handle]
	d.mu.Unlock()
	if found {
		d.actOn(ctx, handle, at)
	}
}

// actOn carries the demand to the volume the handle names, when this
// node staged the handle and no fetch answered the demand yet.
func (d *demanding) actOn(ctx context.Context, handle string, at time.Time) {
	staged := d.node.stagedVolume(handle)
	if staged == nil {
		return
	}
	if !staged.takeDemand(at, time.Now()) {
		return
	}
	if staged.writeable() {
		// While a pod holds a writeable tree, only the application
		// changes it. A demand that pulled would rewrite the pod's files
		// at a moment nobody chose.
		d.logger.InfoContext(ctx, "the demand did nothing",
			"volume", staged.id, "reason", "the volume is writeable")
		return
	}
	d.node.demand(staged)
}

// demand carries a demand to the volume's loop. A volume with pull
// never follows no loop, so a demand does not reach it.
func (n *node) demand(held *volume) {
	if !held.attributes.pull.follows() {
		return
	}
	n.mu.Lock()
	loop := n.loopOf(held)
	n.mu.Unlock()
	if loop == nil {
		return
	}
	loop.demand(held)
}

// loopOf is the loop that follows the volume's repository, and nil
// when none does. The caller holds the node's lock.
func (n *node) loopOf(held *volume) *follower {
	return n.followers[n.store.repository(held.attributes.url).name]
}

// demand records the volume the demand named and wakes the loop. The
// channel has one slot and the send never blocks, so a burst of demands
// never waits on a loop that is fetching.
func (f *follower) demand(held *volume) {
	f.mu.Lock()
	if f.volumes[held.id] != held {
		// The unstage took the volume off the loop after the node
		// found it.
		f.mu.Unlock()
		return
	}
	if f.wanted[held.id] == nil {
		// One count for each volume a pass takes a demand for. Further
		// demands before that pass, and the passes that fetch again
		// after a failure, add no count.
		f.node.readings.demanded(held)
	}
	f.wanted[held.id] = held
	f.mu.Unlock()
	held.reportDemanded(time.Now())
	select {
	case f.demanded <- struct{}{}:
	default:
	}
}

// demandWait is how long a demand waits before the loop pulls: nothing
// until the loop has pulled once, and what is left of
// --demand-min-interval after that.
func (f *follower) demandWait(now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastPull.IsZero() {
		return 0
	}
	return f.node.demandMin - now.Sub(f.lastPull)
}

// answered records when the pass ran, and takes and returns the
// volumes a demand named since the last pass.
func (f *follower) answered(at time.Time) map[string]*volume {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := f.wanted
	f.wanted = map[string]*volume{}
	f.lastPull = at
	return wanted
}

// resumePull is the demand a restart makes. A demand that came while
// the node plugin was down is lost, and a volume with pull on-demand
// has no timer to cover the loss. The caller holds the node's lock.
func (n *node) resumePull(resumed *volume) {
	if !resumed.attributes.pull.follows() {
		return
	}
	if loop := n.loopOf(resumed); loop != nil {
		loop.demand(resumed)
	}
}
