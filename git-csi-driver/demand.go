package main

// demand.go is the channel that pulls a read-only volume from outside
// the node: the annotation on the PersistentVolume, the one watch that
// reads it, and the interval that bounds a burst of demands.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// demandAnnotation is where a demand is written. Any value the node
// has not acted on yet is a demand. A timestamp is the convention,
// because it is what a person reads in kubectl describe.
const demandAnnotation = "git.liken.sh/pull-requested-at"

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
	resync time.Duration

	// acted is the annotation value the node last acted on, by volume
	// handle. A value that differs from it is a demand.
	mu    sync.Mutex
	acted map[string]string
}

func newDemanding(answering *node, client kubernetes.Interface, logger *slog.Logger) *demanding {
	return &demanding{
		node:   answering,
		client: client,
		logger: logger,
		resync: defaultResync,
		acted:  map[string]string{},
	}
}

// follow holds the watch for the driver's whole run. A driver outside
// a cluster holds no client, so it reads no demand.
func (d *demanding) follow(ctx context.Context) {
	if d.client == nil {
		return
	}
	for ctx.Err() == nil {
		d.pass(ctx)
	}
}

// pass reads every PersistentVolume, then holds a watch open until it
// closes or the resync says to read them all again. The list is what
// catches a demand written while no watch was open.
func (d *demanding) pass(ctx context.Context) {
	volumes, err := d.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		// A list the API server refuses costs one call per resync.
		d.logger.WarnContext(ctx, "the volumes were not listed", "error", err)
		d.rest(ctx)
		return
	}
	for i := range volumes.Items {
		d.read(ctx, &volumes.Items[i])
	}

	watching, err := d.client.CoreV1().PersistentVolumes().Watch(ctx, metav1.ListOptions{})
	if err != nil {
		d.logger.WarnContext(ctx, "the volumes are not watched", "error", err)
		d.rest(ctx)
		return
	}
	defer watching.Stop()

	resync := time.NewTimer(d.resync)
	defer resync.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-resync.C:
			return
		case event, open := <-watching.ResultChan():
			if !open {
				return
			}
			held, isVolume := event.Object.(*corev1.PersistentVolume)
			if !isVolume {
				continue
			}
			d.read(ctx, held)
		}
	}
}

// rest waits out the resync after a call that failed.
func (d *demanding) rest(ctx context.Context) {
	timer := time.NewTimer(d.resync)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// read acts on one PersistentVolume. It acts only when the volume is
// this driver's, only when this node staged the handle, and only when
// the annotation carries a value the node has not acted on.
func (d *demanding) read(ctx context.Context, held *corev1.PersistentVolume) {
	source := held.Spec.CSI
	if source == nil || source.Driver != driverName {
		return
	}
	asked := held.Annotations[demandAnnotation]
	if asked == "" {
		return
	}
	staged := d.node.stagedVolume(source.VolumeHandle)
	if staged == nil {
		return
	}
	if !d.acting(source.VolumeHandle, asked) {
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
