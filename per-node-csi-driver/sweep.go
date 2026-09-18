package main

// sweep.go removes the copies of volumes that are gone. A copy is
// removed when its PersistentVolume is deleted, and for no other reason.

import (
	"context"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// defaultResync is the interval at which the informer lists every
// PersistentVolume again, whatever the watch delivered in between.
const defaultResync = 10 * time.Minute

// watchedKind is the resource kind per_node_csi_watch_restarts_total
// reports: the one watch the sweep holds, on the PersistentVolumes of
// this driver.
const watchedKind = "PersistentVolume"

// sweeping is the watch on PersistentVolumes and the pass it wakes. One
// watch covers every copy on the node.
type sweeping struct {
	node   *node
	client kubernetes.Interface
	every  time.Duration
	resync time.Duration
	logger *slog.Logger
}

// newSweeping builds the sweep around the client the driver already
// holds for Events. A nil client is a driver outside a cluster.
func newSweeping(answering *node, client kubernetes.Interface,
	every time.Duration, logger *slog.Logger,
) *sweeping {
	return &sweeping{
		node:   answering,
		client: client,
		every:  every,
		resync: defaultResync,
		logger: logger,
	}
}

// follow keeps the watch for the driver's whole run. A driver outside a
// cluster, and one whose cache never syncs, sweeps nothing. Without
// the list of volumes every copy would look like an orphan.
func (s *sweeping) follow(ctx context.Context) {
	if s.client == nil {
		s.logger.WarnContext(ctx, "no sweep", "reason", "the driver reached no cluster")
		return
	}
	factory := informers.NewSharedInformerFactory(s.client, s.resync)
	informer := factory.Core().V1().PersistentVolumes().Informer()
	deleted := make(chan struct{}, 1)
	s.watchDeletes(ctx, informer, deleted)
	s.watchErrors(informer)
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		s.logger.WarnContext(ctx, "no sweep", "reason", "the volumes did not sync")
		return
	}

	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	s.sweep(ctx, handlesOf(informer.GetStore().List()))
	for {
		select {
		case <-ctx.Done():
			return
		case <-deleted:
			s.sweep(ctx, handlesOf(informer.GetStore().List()))
		case <-ticker.C:
			s.sweep(ctx, handlesOf(informer.GetStore().List()))
		}
	}
}

// watchDeletes wakes a pass the moment a PersistentVolume is deleted, so
// a copy leaves the node in seconds and not on the next tick. The
// channel has one slot and the send never blocks, so a burst of deletes
// costs one pass.
func (s *sweeping) watchDeletes(
	ctx context.Context, informer cache.SharedIndexInformer, wake chan<- struct{},
) {
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(any) {
			select {
			case wake <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		// The tick still finds the orphan, so a handler the informer
		// refused costs latency and nothing more.
		s.logger.WarnContext(ctx, "the deletes are not watched", "error", err)
	}
}

// watchErrors counts a restart every time the reflector's list and
// watch call ends and it opens the watch again, whether the API server
// closed it or refused it. The pass on the tick still finds every
// orphan and every deletion the resync missed, so a handler the
// informer refuses costs a metric and nothing more.
func (s *sweeping) watchErrors(informer cache.SharedIndexInformer) {
	if err := informer.SetWatchErrorHandler(s.handleWatchError); err != nil {
		s.logger.Warn("the watch restarts are not counted", "error", err)
	}
}

// handleWatchError is the reflector's WatchErrorHandler. It is a method
// and not a closure so a test can call it directly, with no real watch
// failure to arrange.
func (s *sweeping) handleWatchError(_ *cache.Reflector, err error) {
	s.node.readings.watchRestarted(watchedKind)
	s.logger.Info("the watch restarted", "error", err)
}

// handlesOf returns the handles that this driver's PersistentVolumes
// name, out of everything in the informer's cache. The filter is here
// because a field selector cannot reach spec.csi.driver.
func handlesOf(objects []any) map[string]bool {
	named := map[string]bool{}
	for _, object := range objects {
		held, isVolume := object.(*corev1.PersistentVolume)
		if !isVolume {
			continue
		}
		source := held.Spec.CSI
		if source == nil || source.Driver != driverName {
			continue
		}
		named[source.VolumeHandle] = true
	}
	return named
}

// sweep removes every copy that no PersistentVolume names and no pod on
// this node holds.
func (s *sweeping) sweep(ctx context.Context, named map[string]bool) {
	held := s.node.heldHandles()
	for _, handle := range s.node.store.handles() {
		if named[handle] || held[handle] {
			continue
		}
		if err := s.node.store.removeCopy(handle); err != nil {
			s.logger.WarnContext(ctx, "the copy stayed", "volume", handle, "error", err)
			continue
		}
		s.node.readings.forget(handle)
		s.logger.InfoContext(ctx, "swept the copy", "volume", handle)
	}
}
