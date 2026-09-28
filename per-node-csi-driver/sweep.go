package main

// sweep.go removes the copies of volumes that are gone. A copy is
// removed when its PersistentVolume is deleted, and for no other reason.

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

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
		logger: logger,
	}
}

// follow keeps the watch for the driver's whole run. A driver outside a
// cluster, and one whose store never holds the first read, sweeps
// nothing. Without the list of volumes every copy would look like an
// orphan.
//
// client-go's reflector runs the watch. It reads every
// PersistentVolume first, then watches from the version that read
// returned, resumes a watch the API server closed, and reads the
// collection again after a 410 Gone. Upstream maintains and tests that
// loop. The informer is built with tools/cache over the typed client,
// which the driver already links for Events. It takes no resync: a
// resync replays the store to the handlers as updates and reads nothing
// from the API server, and the only handler here acts on a delete.
func (s *sweeping) follow(ctx context.Context) {
	if s.client == nil {
		s.logger.WarnContext(ctx, "no sweep", "reason", "the driver reached no cluster")
		return
	}
	deleted := make(chan struct{}, 1)
	store, informer := cache.NewInformerWithOptions(cache.InformerOptions{
		ListerWatcher: cache.ToListWatcherWithWatchListSemantics(s.volumes(), s.client),
		ObjectType:    &corev1.PersistentVolume{},
		Handler: cache.ResourceEventHandlerFuncs{
			DeleteFunc: func(any) { wake(deleted) },
		},
		Transform: dropManagedFields,
	})
	// RunWithContext returns after the last handler call, so the sweep
	// returns only when nothing of the watch still runs.
	var watching sync.WaitGroup
	defer watching.Wait()
	watching.Go(func() { informer.RunWithContext(ctx) })
	select {
	case <-ctx.Done():
		s.logger.WarnContext(ctx, "no sweep", "reason", "the volumes did not sync")
		return
	case <-informer.HasSyncedChecker().Done():
	}

	// The ticker is a backstop for two failures that no event follows.
	// A removeCopy that fails leaves the copy, and nothing wakes a pass
	// to try it again. A copy that a pod still holds when its
	// PersistentVolume is deleted stays, and the unpublish that later
	// drops the hold does not wake a pass. A watch that closes is not
	// one of the two: the reflector lists again and hands every
	// deletion it missed to the delete handler.
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	s.sweep(ctx, handlesOf(store.List()))
	for {
		select {
		case <-ctx.Done():
			return
		case <-deleted:
			s.sweep(ctx, handlesOf(store.List()))
		case <-ticker.C:
			s.sweep(ctx, handlesOf(store.List()))
		}
	}
}

// volumes is the list and the watch of every PersistentVolume. A field
// selector cannot reach spec.csi.driver, so the watch takes every
// driver's PersistentVolumes, and handlesOf keeps this driver's.
//
// Every watch the API server accepts after the first counts as one
// restart on per_node_csi_watch_restarts_total. The reflector asks the
// API server to close each watch after 5 to 10 minutes, so a healthy
// watch counts 6 to 12 restarts an hour. A refused watch is not
// counted. The reflector opens one watch at a time, and the flag is
// atomic all the same, so a change in client-go that opens watches
// from two goroutines makes no data race here.
func (s *sweeping) volumes() *cache.ListWatch {
	volumes := s.client.CoreV1().PersistentVolumes()
	var opened atomic.Bool
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return volumes.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			watching, err := volumes.Watch(ctx, options)
			if err == nil && opened.Swap(true) {
				s.node.readings.watchRestarted(watchedKind)
			}
			return watching, err
		},
	}
}

// wake sends on a channel with one slot and never blocks, so a burst of
// deletes costs one pass, and a copy leaves the node in seconds and not
// on the next tick.
func wake(pass chan<- struct{}) {
	select {
	case pass <- struct{}{}:
	default:
	}
}

// dropManagedFields removes metadata.managedFields from each object
// before the informer stores it. The field records which client set
// each field of the object. The driver never reads it, and without the
// transform the store holds a copy of it for every PersistentVolume in
// the cluster.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(metav1.Object); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
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
