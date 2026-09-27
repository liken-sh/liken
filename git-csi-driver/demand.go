package main

// demand.go is the channel that pulls a read-only volume from outside
// the node: the annotation on the PersistentVolume, the one list and
// watch that read it, and the interval that bounds a burst of demands.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// demandAnnotation is where a demand is written. Any value the node
// has not acted on yet is a demand. A timestamp is the convention,
// because it is what a person reads in kubectl describe.
const demandAnnotation = "git.liken.sh/pull-requested-at"

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
	retry  time.Duration

	// acted is the annotation value the node last acted on, by volume
	// handle. A value that differs from it is a demand. seen is the
	// last value the node read, by handle, whether or not this node
	// staged the handle.
	mu    sync.Mutex
	acted map[string]string
	seen  map[string]string
}

func newDemanding(answering *node, client kubernetes.Interface, logger *slog.Logger) *demanding {
	return &demanding{
		node:   answering,
		client: client,
		logger: logger,
		retry:  defaultRetry,
		acted:  map[string]string{},
		seen:   map[string]string{},
	}
}

// follow holds the list and the watch for the driver's whole run. A
// driver outside a cluster holds no client, so it reads no demand. The
// list reads every PersistentVolume once, which catches a demand
// written while no watch was open, and the watch carries every demand
// after it.
func (d *demanding) follow(ctx context.Context) {
	if d.client == nil {
		return
	}
	volumes := d.client.CoreV1().PersistentVolumes()
	(&listWatch{
		kind: persistentVolumeKind,
		list: func(ctx context.Context) (string, error) {
			held, err := volumes.List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", err
			}
			// The list is the whole state, so a value for a volume it
			// no longer holds belongs to a deleted PersistentVolume.
			d.mu.Lock()
			d.seen = map[string]string{}
			d.mu.Unlock()
			for i := range held.Items {
				d.read(ctx, &held.Items[i])
			}
			d.keepListed(held.Items)
			return held.ResourceVersion, nil
		},
		watch: volumes.Watch,
		act: func(ctx context.Context, event watch.Event) error {
			held, isVolume := event.Object.(*corev1.PersistentVolume)
			switch {
			case !isVolume:
			case event.Type == watch.Deleted:
				d.forget(held)
			default:
				// A bookmark is a PersistentVolume with no spec, so
				// read passes over it.
				d.read(ctx, held)
			}
			return nil
		},
		retry:    d.retry,
		logger:   d.logger,
		readings: d.node.readings,
	}).follow(ctx)
}

// read acts on one PersistentVolume. It acts only when the volume is
// this driver's, only when this node staged the handle, and only when
// the annotation carries a value the node has not acted on. It records
// the value for a handle this node has not staged, because a stage
// fetches before it adds the volume to the node, and a demand that
// arrives between the two is read again when the stage ends.
func (d *demanding) read(ctx context.Context, held *corev1.PersistentVolume) {
	source := held.Spec.CSI
	if source == nil || source.Driver != driverName {
		return
	}
	asked := held.Annotations[demandAnnotation]
	if asked == "" {
		return
	}
	d.mu.Lock()
	d.seen[source.VolumeHandle] = asked
	d.mu.Unlock()
	d.actOn(ctx, source.VolumeHandle, asked)
}

// forget drops the values the node read and acted on for a deleted
// PersistentVolume. The maps then hold one entry for each
// PersistentVolume that exists, not for every one the driver ever read,
// and a PersistentVolume created again with the same handle and the
// same value is a new demand.
func (d *demanding) forget(held *corev1.PersistentVolume) {
	if held.Spec.CSI == nil {
		return
	}
	d.mu.Lock()
	delete(d.seen, held.Spec.CSI.VolumeHandle)
	delete(d.acted, held.Spec.CSI.VolumeHandle)
	d.mu.Unlock()
}

// keepListed drops the acted value of every handle the list does not
// hold. A handle the list holds keeps its value, so a list does not act
// again on a demand the node already acted on.
func (d *demanding) keepListed(listed []corev1.PersistentVolume) {
	present := map[string]bool{}
	for i := range listed {
		if source := listed[i].Spec.CSI; source != nil {
			present[source.VolumeHandle] = true
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for handle := range d.acted {
		if !present[handle] {
			delete(d.acted, handle)
		}
	}
}

// arrived acts on the last demand the node read for a volume a stage
// just added to the node. The watch sends no event for that demand
// again, so without this read the volume keeps its old commit until
// the annotation changes.
func (d *demanding) arrived(ctx context.Context, handle string) {
	d.mu.Lock()
	asked, found := d.seen[handle]
	d.mu.Unlock()
	if found {
		d.actOn(ctx, handle, asked)
	}
}

// actOn carries the demand to the volume the handle names, once per
// value, when this node staged the handle.
func (d *demanding) actOn(ctx context.Context, handle, asked string) {
	staged := d.node.stagedVolume(handle)
	if staged == nil {
		return
	}
	if !d.acting(handle, asked) {
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

// acting reports whether the value differs from the one the node last
// acted on for the handle, and records it as acted on.
func (d *demanding) acting(handle, asked string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.acted[handle] == asked {
		return false
	}
	d.acted[handle] = asked
	return true
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
	held.reportDemanded(time.Now())
	f.mu.Lock()
	f.wanted[held.id] = held
	f.mu.Unlock()
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

// answered records when the pass ran and counts one demanded pull for
// every volume a demand named since the last pass.
func (f *follower) answered(at time.Time) {
	f.mu.Lock()
	wanted := f.wanted
	f.wanted = map[string]*volume{}
	f.lastPull = at
	f.mu.Unlock()
	for _, held := range wanted {
		f.node.readings.demanded(held)
	}
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
