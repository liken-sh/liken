package main

// Two watches wake the pass: one of every Person, and one of every
// baker pod in every namespace. Both run on client-go's reflector,
// through the shared informer package. A handler only wakes the pass,
// and the pass reads what it needs.

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

// recheckInterval is the slow check. An HTTP URL has no watch, and
// neither has a file on NFS or on a claim, so a picture replaced at the
// same source changes nothing in the Person and wakes no pass. Every
// six hours the pass reads each such source again: a conditional GET
// that costs a 304 when the picture has not changed, or a baker pod
// that writes the status only when the file's time or size changed. A
// person who wants the new picture at once sets the check-avatar
// annotation.
const recheckInterval = 6 * time.Hour

// retryDelay is the wait after a pass that failed, such as a status
// write the API server refused while it restarts.
const retryDelay = 30 * time.Second

// run watches until the context ends, and runs a pass for each wake.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	wake := make(chan struct{}, 1)
	signal := func() { poke(wake) }

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
			<-people.Done()
			<-pods.Done()
			return
		case <-wake:
		case <-recheck.C:
			rechecking = true
		case <-deadline:
		case <-retry:
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

// poke wakes the pass. The channel holds one wake, so a burst of
// events makes one pass.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// personMark is the part of a Person whose change needs a pass: its
// spec, through the generation, and the check-avatar annotation. A
// status write changes neither, so the operator's own writes wake
// nothing. The UID is in the mark because a Person deleted and created
// again under one name while the watch was down arrives as an update.
func personMark(item person) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.checkRequest())
}

// podMark is the part of a baker pod whose change needs a pass: its
// phase, and the start of its deletion.
func podMark(item pod) string {
	return item.Status.Phase + " " + item.Metadata.DeletionTimestamp
}

// markHandler wakes the pass for a new object, a removed object, and
// an update that changes the object's mark. An object that does not
// convert is reported and wakes the pass, because one extra pass costs
// less than a missed edit.
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
