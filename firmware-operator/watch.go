package main

// The operator watches Firmware, FirmwareCatalog, and Machine objects,
// the ResourceSlices that machine-operator publishes, the pods that
// run uploads, scans, rehearsals, and applies in the operator's
// namespace, and the Disruptions that the operator created. The
// watches run on client-go's reflector, through the shared informer
// package. A watch handler only wakes the pass, and the pass reads what
// it needs from the stores.
//
// Each timer is a deadline: of an upload, a scan, a rehearsal, or an
// apply in progress, or the wait before a failed pass runs again. The
// exception is the read of each FirmwareCatalog's jcat once per
// -catalog-interval, because LVFS publishes no feed of its changes
// (catalogfetch.go).

import (
	"context"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

var (
	firmwaresResource   = schema.GroupVersionResource{Group: "firmware.liken.sh", Version: "v1alpha1", Resource: "firmwares"}
	catalogsResource    = schema.GroupVersionResource{Group: "firmware.liken.sh", Version: "v1alpha1", Resource: "firmwarecatalogs"}
	slicesResource      = schema.GroupVersionResource{Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices"}
	podsResource        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	machinesResource    = schema.GroupVersionResource{Group: "liken.sh", Version: "v1alpha1", Resource: "machines"}
	disruptionsResource = schema.GroupVersionResource{Group: "liken.sh", Version: "v1alpha1", Resource: "disruptions"}
)

// retryDelay is how long the operator waits after a failed pass, such
// as a status write that the API server refused while it restarted.
const retryDelay = 30 * time.Second

// run starts the watches and runs a pass each time one wakes it, until
// ctx ends. It returns after the pass in progress finishes and every
// watch has stopped, so the caller can release the Lease when it
// returns.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	wake := make(chan struct{}, 1)
	signal := func() { wakePass(wake) }
	start := func(source informer.Source, handler cache.ResourceEventHandler) *informer.Collection {
		return informer.Start(ctx, watcher, source, informer.Options{Handler: handler, Synced: signal})
	}

	firmwares := start(informer.Source{Resource: firmwaresResource}, markHandler(signal, firmwareMark))
	// The API server filters by driver, so the slices of other drivers,
	// such as a GPU vendor's, never reach the operator.
	slices := start(informer.Source{Resource: slicesResource, FieldSelector: "spec.driver=" + driverName}, markHandler(signal, sliceMark))
	pods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: uploadLabel + "=" + uploadLabelValue},
		markHandler(signal, podMark))
	scanPods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: scanLabel + "=" + scanLabelValue},
		markHandler(signal, podMark))
	rehearsalPods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: rehearsalLabel + "=" + rehearsalLabelValue},
		markHandler(signal, podMark))
	applyPods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: applyLabel + "=" + applyLabelValue},
		markHandler(signal, applyPodMark))
	machines := start(informer.Source{Resource: machinesResource}, markHandler(signal, machineMark))
	catalogs := start(informer.Source{Resource: catalogsResource}, markHandler(signal, catalogMark))
	// A cluster on a liken release without the Disruption kind returns
	// 404 for the collection. Absent makes the watch treat a 404 as an
	// empty collection, so the operator runs without requesting reboot
	// turns, and the watch delivers Disruptions once the cluster serves
	// the kind.
	disruptions := informer.Start(ctx, watcher, informer.Source{Resource: disruptionsResource, LabelSelector: disruptionLabel},
		informer.Options{Handler: markHandler(signal, disruptionMark), Synced: signal, Absent: apierrors.IsNotFound})
	o.firmwares = informer.Held{View: whole(firmwares), Versions: memo.New()}
	o.catalogs = informer.Held{View: whole(catalogs), Versions: memo.New()}
	o.slices, o.pods, o.scanPods, o.rehearsalPods, o.machines = slices.View(), pods.View(), scanPods.View(), rehearsalPods.View(), machines.View()
	o.applyPods, o.disruptions = applyPods.View(), disruptions.View()

	var deadline, retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
		case <-wake:
		case <-deadline:
		case <-retry:
		}
		// select chooses at random among ready cases, so a wake can win
		// over ctx.Done(). Checking ctx here keeps a stopped operator
		// from starting another pass.
		if ctx.Err() != nil {
			for _, watch := range []*informer.Collection{firmwares, slices, pods, scanPods, rehearsalPods, applyPods, machines, catalogs, disruptions} {
				<-watch.Done()
			}
			return
		}
		next, err := o.pass(ctx)
		retry = nil
		if err != nil {
			fmt.Fprintf(os.Stderr, "firmware-operator: %v\n", err)
			retry = time.After(retryDelay)
		}
		deadline = nil
		if !next.IsZero() {
			deadline = time.After(time.Until(next))
		}
	}
}

// whole returns the view of a watch that has no selector. Setting Whole
// makes a list through the view also include objects the operator
// created that the store has not received yet.
func whole(watch *informer.Collection) informer.View {
	view := watch.View()
	view.Whole = true
	return view
}

// wakePass wakes the pass. The channel buffers one wake, so a burst of
// events causes one pass.
func wakePass(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// firmwareMark is the part of a Firmware whose change needs a pass: its
// spec (through metadata.generation) and its deletion timestamp. A
// status write changes neither, so the operator's own writes do not
// wake the pass. The UID is included because a Firmware that was
// deleted and created again under the same name while the watch was
// down arrives as an update.
func firmwareMark(item firmware) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp)
}

// sliceMark is the slice's UID and its devices. The mark compares the
// devices themselves, not the pool's generation, because a writer can
// change a device without incrementing the generation.
func sliceMark(item resourceSlice) string {
	return string(mustJSON([]any{item.Metadata.UID, item.Spec.Devices}))
}

// catalogMark is the part of a FirmwareCatalog whose change needs a
// pass, chosen for the same reasons as firmwareMark.
func catalogMark(item catalog) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp)
}

// podMark is the part of a pod whose change needs a pass: its phase,
// whether it is scheduled, and its deletion timestamp.
func podMark(item pod) string {
	return fmt.Sprint(item.Status.Phase, item.scheduled(), item.Metadata.DeletionTimestamp)
}

// applyPodMark is podMark plus the apply pod's Ready condition. The
// condition becomes True when the pod has finished preparing, and the
// pass then requests the machine's reboot turn.
func applyPodMark(item pod) string {
	return fmt.Sprint(podMark(item), item.ready())
}

// machineMark is the part of a Machine that a pass reads: the labels
// that spec.machines selects on, and the phase and boot time that
// determine whether to scan.
func machineMark(item machine) string {
	return fmt.Sprint(item.Metadata.Labels, item.Status.Phase, item.Status.Boot.Time)
}

// markHandler wakes the pass when an object is added or deleted, and
// when an update changes the object's mark. When an object does not
// convert to T, the handler reports it and wakes the pass anyway,
// because an extra pass costs less than a missed change.
func markHandler[T any](signal func(), mark func(T) string) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { signal() },
		DeleteFunc: func(any) { signal() },
		UpdateFunc: func(previous, current any) {
			before, err := informer.Convert[T](previous)
			if err != nil {
				informer.Report("an earlier copy", err)
				signal()
				return
			}
			after, err := informer.Convert[T](current)
			if err != nil {
				informer.Report("a new copy", err)
				signal()
				return
			}
			if mark(before) != mark(after) {
				signal()
			}
		},
	}
}
