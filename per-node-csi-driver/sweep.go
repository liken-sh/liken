package main

// sweep.go deletes a copy when no PersistentVolume of this driver has
// its handle and no pod on this node mounts it.

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

// watchedKind is the value of the kind label on
// per_node_csi_watch_restarts_total.
const watchedKind = "PersistentVolume"

type sweeper struct {
	node   *node
	client kubernetes.Interface
	every  time.Duration
	logger *slog.Logger
}

// newSweeper is passed the client that the driver posts Events with,
// which is nil when the driver runs outside a cluster.
func newSweeper(n *node, client kubernetes.Interface,
	every time.Duration, logger *slog.Logger,
) *sweeper {
	return &sweeper{
		node:   n,
		client: client,
		every:  every,
		logger: logger,
	}
}

// run runs the sweep until ctx is done. It sweeps nothing when the
// client is nil, or when ctx ends before the informer has listed the
// PersistentVolumes. Without that list, the sweep would delete every
// copy that no pod mounts.
//
// A client-go informer keeps the list current. Its reflector lists the
// PersistentVolumes, watches from the version of that list, reopens a
// closed watch, and lists again after a 410 Gone. The informer has no
// resync period, because a resync only replays cached objects to the
// handler as updates, and the handler acts only on deletes.
func (s *sweeper) run(ctx context.Context) {
	if s.client == nil {
		s.logger.WarnContext(ctx, "Not sweeping the copies", "reason", "the driver has no Kubernetes client")
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
	// RunWithContext returns after its last handler call, so waiting for
	// it means no informer goroutine outlives run.
	var watching sync.WaitGroup
	defer watching.Wait()
	watching.Go(func() { informer.RunWithContext(ctx) })
	select {
	case <-ctx.Done():
		s.logger.WarnContext(ctx, "Not sweeping the copies", "reason", "the PersistentVolume list did not sync before the driver stopped")
		return
	case <-informer.HasSyncedChecker().Done():
	}

	// The ticker sweeps in the cases that send no event: a copy that
	// removeCopy failed to delete, and a copy that a pod still mounted
	// when its PersistentVolume was deleted, because the pod's later
	// unpublish does not start a sweep. A closed watch needs no ticker,
	// because the reflector lists again and calls the delete handler for
	// each deletion it missed.
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

// volumes lists and watches all PersistentVolumes. A field selector
// cannot match spec.csi.driver, so the watch includes other drivers'
// volumes and handlesOf filters them out.
//
// Each successful watch after the first increments
// per_node_csi_watch_restarts_total, and a failed watch request does
// not. The reflector sets a random server timeout of 5 to 10 minutes on
// each watch, so a restart is routine. The flag is atomic so that
// concurrent watch calls cannot race.
func (s *sweeper) volumes() *cache.ListWatch {
	volumes := s.client.CoreV1().PersistentVolumes()
	var opened atomic.Bool
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return volumes.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			watching, err := volumes.Watch(ctx, options)
			if err == nil && opened.Swap(true) {
				s.node.metrics.watchRestarted(watchedKind)
			}
			return watching, err
		},
	}
}

// wake signals a sweep without blocking. The channel holds one pending
// signal, so a burst of deletes causes one sweep.
func wake(pending chan<- struct{}) {
	select {
	case pending <- struct{}{}:
	default:
	}
}

// dropManagedFields clears metadata.managedFields before the informer
// caches the object, because the driver never reads the field and the
// cache would only spend memory on it.
func dropManagedFields(object any) (any, error) {
	if item, ok := object.(metav1.Object); ok {
		item.SetManagedFields(nil)
	}
	return object, nil
}

func handlesOf(objects []any) map[string]bool {
	named := map[string]bool{}
	for _, object := range objects {
		volume, isVolume := object.(*corev1.PersistentVolume)
		if !isVolume {
			continue
		}
		source := volume.Spec.CSI
		if source == nil || source.Driver != driverName {
			continue
		}
		named[source.VolumeHandle] = true
	}
	return named
}

func (s *sweeper) sweep(ctx context.Context, named map[string]bool) {
	mounted := s.node.publishedHandles()
	for _, handle := range s.node.store.handles() {
		if named[handle] || mounted[handle] {
			continue
		}
		if err := s.node.store.removeCopy(handle); err != nil {
			s.logger.WarnContext(ctx, "Could not delete the copy, and the next sweep retries", "volume", handle, "error", err)
			continue
		}
		s.node.metrics.forget(handle)
		s.logger.InfoContext(ctx, "Deleted the copy of a volume that no PersistentVolume names", "volume", handle)
	}
}
