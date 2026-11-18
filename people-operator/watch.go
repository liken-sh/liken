package main

// A watch on every Person and a watch on the baker pods in every
// namespace start passes. The event handlers only request a pass, and
// the pass reads the state it needs.

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
	peopleResource = schema.GroupVersionResource{Group: "people.liken.sh", Version: "v1alpha1", Resource: "people"}
	podsResource   = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// recheckInterval is how often the operator reads every source except
// data: again. It cannot watch an HTTP URL, an NFS file, or a file on a
// claim, so a picture replaced at the same URI changes nothing in the
// Person. An HTTP recheck is a conditional GET. A file recheck runs a
// baker pod, and the operator compares the file's modification time and
// size with the last read. So an unchanged picture causes no status
// write. To read a picture sooner,
// change the check-avatar annotation.
const recheckInterval = 6 * time.Hour

// retryDelay is the wait before the pass that follows a failed pass,
// for example one whose status write failed while the API server
// restarted.
const retryDelay = 30 * time.Second

// run starts the watches and runs a pass for each wake until ctx ends.
// It returns after the running pass finishes and both watches stop, so
// the caller can release the Lease on return.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	wake := make(chan struct{}, 1)
	signal := func() { requestPass(wake) }

	people := informer.Start(ctx, watcher, informer.Source{Resource: peopleResource}, informer.Options{
		Handler: markHandler(signal, personMark),
		Synced:  signal,
	})
	pods := informer.Start(ctx, watcher, informer.Source{Resource: podsResource, LabelSelector: bakerLabel + "=" + bakerLabelValue}, informer.Options{
		Handler: markHandler(signal, podMark),
		Synced:  signal,
	})
	view := people.View()
	view.Whole = true
	o.people = informer.Held{View: view, Versions: memo.New()}
	o.bakers.pods = pods.View()

	recheck := time.NewTicker(recheckInterval)
	defer recheck.Stop()
	var deadline, retry <-chan time.Time
	rechecking := false
	for {
		select {
		case <-ctx.Done():
		case <-wake:
		case <-recheck.C:
			rechecking = true
		case <-deadline:
		case <-retry:
		}
		// select chooses randomly among ready cases, so a wake that
		// arrives together with ctx.Done may be chosen. Check ctx here so
		// that no pass starts after the stop.
		if ctx.Err() != nil {
			<-people.Done()
			<-pods.Done()
			return
		}
		next, err := o.pass(ctx, rechecking)
		retry = nil
		if err != nil {
			fmt.Fprintf(os.Stderr, "people-operator: %v\n", err)
			retry = time.After(retryDelay)
		} else {
			rechecking = false
		}
		deadline = nil
		if !next.IsZero() {
			deadline = time.After(time.Until(next))
		}
	}
}

// requestPass does not block. The channel buffers one signal, so a
// burst of events leads to one pass.
func requestPass(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// personMark returns the parts of a Person whose change starts a pass:
// the generation, which changes with the spec, and the check-avatar
// annotation. A status write changes neither, so the operator's own
// writes start no pass. The mark includes the UID because, after a gap
// in the watch, a Person deleted and created again under the same name
// arrives as an update.
func personMark(item person) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.checkRequest())
}

func podMark(item pod) string {
	return item.Status.Phase + " " + item.Metadata.DeletionTimestamp
}

// markHandler starts a pass when an object is added or deleted, and
// when an update changes the object's mark. An object that fails to
// convert is reported and also starts a pass, because an unneeded pass
// costs less than a missed change.
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
