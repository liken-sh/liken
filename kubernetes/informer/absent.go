package informer

// A kind can be defined by another operator, which a cluster may not
// run. The watch of such a kind reads the API server's 404 as an empty
// collection, so the operator starts and runs on a cluster without the
// definition, and a pass reads the kind from its copy with no request.
//
// Nothing the operator watches reports that a definition arrived. So
// the watch of an absent collection is a quiet stream that sends no
// event until the recheck is due, and then a 410 Gone. The reflector
// then reads the collection again, which finds it once it exists. The
// 410 makes that read a list, which answers a resourceVersion to watch
// from. A stream that closed with no event would make the reflector
// watch from the empty version of the empty list, and a watch from no
// version reports no object deleted before it opened. Without the quiet
// stream, the reflector would back off, list again, and log a failure
// about every 30 seconds for as long as the definition is missing.

import (
	"context"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// defaultAbsentRecheck is the wait of Options.AbsentRecheck when it is
// zero. A definition arrives when a person installs an operator, so a
// wait of minutes costs one request and delays nothing a person waits
// on.
const defaultAbsentRecheck = 5 * time.Minute

// absence is how one watch treats an absent collection. The zero value
// treats every refusal as a failure.
type absence struct {
	refused func(error) bool
	recheck time.Duration
}

// absence reads the options once, before the reflector starts, so the
// reflector's goroutines read no shared setting.
func (o Options) absence() absence {
	recheck := o.AbsentRecheck
	if recheck == 0 {
		recheck = defaultAbsentRecheck
	}
	return absence{refused: o.Absent, recheck: recheck}
}

func (a absence) is(err error) bool {
	return err != nil && a.refused != nil && a.refused(err)
}

// list answers an empty collection for a list the API server refused
// because the collection is absent.
func (a absence) list(items runtime.Object, err error) (runtime.Object, error) {
	if a.is(err) {
		return &unstructured.UnstructuredList{}, nil
	}
	return items, err
}

// watch answers the quiet stream for a watch the API server refused
// because the collection is absent. The reflector's first read is a
// streaming list, which is a watch that asks for the initial events.
// Its refusal stays a refusal, so the reflector falls back to the list
// above, which answers the empty collection.
func (a absence) watch(ctx context.Context, request metav1.ListOptions, err error) (watch.Interface, bool) {
	if !a.is(err) || request.SendInitialEvents != nil {
		return nil, false
	}
	return quietWatch(ctx, a.recheck), true
}

// quietWatch sends no event until the recheck is due, and then a 410
// Gone. It ends when the context ends.
func quietWatch(ctx context.Context, recheck time.Duration) watch.Interface {
	stream := watch.NewRaceFreeFake()
	go func() {
		timer := time.NewTimer(recheck)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			stream.Stop()
		case <-timer.C:
			// The reflector stops the stream once it reads the error. A
			// stream it stopped already takes no event.
			stream.Error(&metav1.Status{
				Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired,
				Message: "the collection was absent; read it again",
			})
		}
	}()
	return stream
}
