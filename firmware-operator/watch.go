package main

// Six watches wake the pass: one of every Firmware, one of every
// Catalog, one of the ResourceSlices that machine-operator publishes,
// one each of the upload pods and the scan pods in the operator's
// namespace, and one of every Machine. All six run on client-go's
// reflector, through the shared informer package. A handler only wakes
// the pass, and the pass reads what it needs. The timers are clocks:
// the deadline of an upload or a scan in progress, and the wait after
// a pass that failed. The one timer that is not a clock is the read of
// each Catalog's jcat once an interval, because LVFS publishes no feed
// of its changes (catalogfetch.go).

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

var (
	firmwaresResource = schema.GroupVersionResource{Group: "firmware.liken.sh", Version: "v1alpha1", Resource: "firmwares"}
	catalogsResource  = schema.GroupVersionResource{Group: "firmware.liken.sh", Version: "v1alpha1", Resource: "catalogs"}
	slicesResource    = schema.GroupVersionResource{Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices"}
	podsResource      = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	machinesResource  = schema.GroupVersionResource{Group: "liken.sh", Version: "v1alpha1", Resource: "machines"}
)

// retryDelay is the wait after a pass that failed, such as a status
// write the API server refused while it restarts.
const retryDelay = 30 * time.Second

// run watches until the context ends, and runs a pass for each wake.
// It returns after the pass in flight, once the watches have ended, so
// the caller may release the Lease when it returns.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	wake := make(chan struct{}, 1)
	signal := func() { poke(wake) }
	start := func(source informer.Source, handler cache.ResourceEventHandler) *informer.Collection {
		return informer.Start(ctx, watcher, source, informer.Options{Handler: handler, Synced: signal})
	}

	firmwares := start(informer.Source{Resource: firmwaresResource}, markHandler(signal, firmwareMark))
	// The API server filters the slices by driver, so a GPU vendor's
	// slices never reach the operator.
	slices := start(informer.Source{Resource: slicesResource, FieldSelector: "spec.driver=" + driverName}, markHandler(signal, sliceMark))
	pods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: uploadLabel + "=" + uploadLabelValue},
		markHandler(signal, podMark))
	scanPods := start(informer.Source{Resource: podsResource, Namespace: o.namespace, LabelSelector: scanLabel + "=" + scanLabelValue},
		markHandler(signal, podMark))
	machines := start(informer.Source{Resource: machinesResource}, markHandler(signal, machineMark))
	catalogs := start(informer.Source{Resource: catalogsResource}, markHandler(signal, catalogMark))
	o.firmwares = informer.Held{View: whole(firmwares), Versions: memo.New()}
	o.catalogs = informer.Held{View: whole(catalogs), Versions: memo.New()}
	o.slices, o.pods, o.scanPods, o.machines = slices.View(), pods.View(), scanPods.View(), machines.View()

	var deadline, retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
		case <-wake:
		case <-deadline:
		case <-retry:
		}
		// select picks at random among the cases that are ready, so a
		// wake that came with the stop is checked here and starts no
		// pass.
		if ctx.Err() != nil {
			for _, watch := range []*informer.Collection{firmwares, slices, pods, scanPods, machines, catalogs} {
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

// whole answers the store of a watch with no selector, whose list also
// holds each object the operator created that the store does not hold
// yet.
func whole(watch *informer.Collection) informer.View {
	view := watch.View()
	view.Whole = true
	return view
}

// poke wakes the pass. The channel holds one wake, so a burst of
// events makes one pass.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// firmwareMark is the part of a Firmware whose change needs a pass: its
// spec, through the generation, and the start of its deletion. A
// status write changes neither, so the operator's own writes wake
// nothing. The UID is in the mark because a Firmware deleted and
// created again under one name while the watch was down arrives as an
// update.
func firmwareMark(item firmware) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp)
}

// sliceMark is the slice's devices. machine-operator moves the pool's
// generation on each write, but a mark on the generation alone would
// miss a device that a writer changed under the same generation, so
// the mark holds the devices themselves. A slice holds a few dozen
// devices, so the comparison is cheap.
func sliceMark(item resourceSlice) string {
	return string(mustJSON([]any{item.Metadata.UID, item.Spec.Devices}))
}

// catalogMark is the part of a Catalog whose change needs a pass, for
// the reasons of firmwareMark.
func catalogMark(item catalog) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp)
}

// podMark is the part of an upload or a scan pod whose change needs a
// pass: its phase, its placement, and the start of its deletion.
func podMark(item pod) string {
	return fmt.Sprint(item.Status.Phase, item.scheduled(), item.Metadata.DeletionTimestamp)
}

// machineMark is the part of a Machine that a pass reads: the labels
// that spec.machines selects, and the phase and the boot time that
// decide a scan.
func machineMark(item machine) string {
	return fmt.Sprint(item.Metadata.Labels, item.Status.Phase, item.Status.Boot.Time)
}

// markHandler wakes the pass for a new object, a removed object, and
// an update that changes the object's mark. An object that does not
// convert is reported and wakes the pass, because one extra pass costs
// less than a missed change.
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
