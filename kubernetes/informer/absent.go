package informer

// A kind can come from a CRD that another operator installs, and the
// cluster may not run that operator. When Options.Absent matches the
// list error, such as a 404, the informer stores an empty collection
// and reports it synced. The operator then starts, and its passes read
// the empty store without a request to the API server.
//
// No watch reports that a CRD was installed, so the informer must list
// again to find out. After the empty list, the reflector gets
// quietWatch in place of a real watch. quietWatch sends nothing until
// Options.AbsentRecheck has passed and then sends 410 Gone, which makes
// the reflector list the collection again. A real watch would fail with
// 404, and the reflector would back off, list again, and log the
// failure about every 30 seconds while the CRD is missing.
//
// A real watch is also unsafe after the empty list. The list is built
// here and has no resourceVersion, so the watch would open with none.
// A watch with no resourceVersion sends the objects that exist when it
// opens and reports no deletion made before then. If the CRD were
// installed between the list and the watch, the API server would accept
// that watch, the reflector would resume from no version, and an object
// deleted while no watch was open would stay in the store.

import (
	"context"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// defaultAbsentRecheck is the interval that Options.AbsentRecheck uses
// when it is zero. A CRD appears when a person installs an operator, so
// a few minutes is soon enough, and it costs one list request per
// interval.
const defaultAbsentRecheck = 5 * time.Minute

// absence is the part of Options that handles an absent collection. In
// the zero value, matches is nil, so every list error is a failure.
type absence struct {
	matches func(error) bool
	recheck time.Duration
}

// absence copies the options once, before the reflector starts, so the
// reflector's goroutines do not read Options concurrently.
func (o Options) absence() absence {
	recheck := o.AbsentRecheck
	if recheck == 0 {
		recheck = defaultAbsentRecheck
	}
	return absence{matches: o.Absent, recheck: recheck}
}

func (a absence) is(err error) bool {
	return err != nil && a.matches != nil && a.matches(err)
}

// list returns an empty collection in place of an error that means the
// collection is absent.
func (a absence) list(items runtime.Object, err error) (runtime.Object, error) {
	if a.is(err) {
		return &unstructured.UnstructuredList{}, nil
	}
	return items, err
}

// watch returns quietWatch and true for the watch that the reflector
// opens after the empty list, which has no resourceVersion. It returns
// false for every other watch, which then goes to the API server. That
// includes the reflector's first read, a streaming list with
// sendInitialEvents set. The API server refuses the streaming list of
// an absent collection, and the reflector falls back to a plain list.
func (a absence) watch(ctx context.Context, request metav1.ListOptions) (watch.Interface, bool) {
	if a.matches == nil || request.ResourceVersion != "" || request.SendInitialEvents != nil {
		return nil, false
	}
	return quietWatch(ctx, a.recheck), true
}

// quietWatch returns a watch that sends nothing for recheck and then
// sends 410 Gone. It stops when ctx ends.
func quietWatch(ctx context.Context, recheck time.Duration) watch.Interface {
	stream := watch.NewRaceFreeFake()
	go func() {
		timer := time.NewTimer(recheck)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			stream.Stop()
		case <-timer.C:
			// The reflector stops the stream after it reads
			// the error, and Error does nothing on a stream
			// that was stopped.
			stream.Error(&metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired,
				Message: "the collection was absent and may exist now, so list it again",
			})
		}
	}()
	return stream
}
