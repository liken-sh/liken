// Package events posts Kubernetes Events about the objects a liken
// component manages, so `kubectl describe` shows a person what
// happened to an object in the last hour.
//
// A status condition answers "what is true now", for automation. An
// Event answers "what just happened", for a person. A log line holds
// every attempt and detail. So a component posts one Event for each
// condition transition (SetCondition, or Transition after a status
// write) and one for each action it takes that changes no condition,
// such as a pod created again or a reboot requested. It never posts a
// reading, a key press, or each retry of a retry loop.
//
// A component builds one Recorder in main and passes it down:
//
//	recorder := events.New(ctx, client, "observatory-operator", events.Options{})
//	recorder.Warning(object, "StepFailed", "Connect failed: the mount did not answer")
//	recorder.SetCondition(object, &status.Conditions, ready, conditions.False)
//
// The recorder writes core/v1 Events through apiclient, and imports
// nothing from k8s.io, so a build that must not link client-go can
// link it. It leaves out eventTime, because the API server applies the
// strict checks of events.k8s.io/v1, which refuse a note longer than
// 1024 bytes, only to an Event that states one. `kubectl describe` and
// `kubectl events` read core/v1.
//
// A write is best effort. Normal and Warning put the Event on a
// bounded queue and return at once, and one goroutine writes the
// queue, so a reconcile pass never waits on an Event and never fails
// because of one. The goroutine sends a failed write again (attempts),
// and logs one line when the last attempt fails. A full queue drops
// the Event and counts it (Dropped).
//
// A repeat of the same Event on the same object within the window
// patches count and lastTimestamp on the Event the recorder wrote, in
// place of a new Event, so `kubectl describe` prints one line such as
// "(x37 over 1h)" in place of 37 lines.
//
// The API server deletes an Event one hour after its last write
// (kube-apiserver's --event-ttl, which k3s does not change). So a
// condition or a status field, not an Event, holds a fact that must
// last longer.
//
// The Events about a cluster-scoped object go in the namespace
// default, because the API server accepts them only in default or
// kube-system. `kubectl describe` finds them there. `kubectl events
// --for` finds them only with -n default or -A.
//
// A component needs the RBAC verbs create and patch on events in each
// namespace it posts to.
//
// The recorder's goroutine ends with the context New takes, and a
// request in flight then ends at once. An Event still queued is lost,
// and a request that the client cut off can still land on the API
// server after the goroutine returns. A component that must know when
// its last write has returned, such as an elected operator that
// releases its Lease after its last write, passes New a context that
// outlives its last pass, and calls Shutdown after that pass:
//
//	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
//	defer cancel()
//	if recorder.Shutdown(ctx) == nil {
//		lead.StepDown()
//	}
//
// A test serves eventstest.Events in front of its fake API server,
// through apiservertest, and reads what the recorder wrote from it. A
// test passes New t.Context().
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// queueLength bounds the Events that wait to be written. A component
// posts a few Events for each object in an hour, so a full queue
// means the API server stopped answering, and the recorder drops new
// Events instead of holding memory for them.
const queueLength = 256

// attempts is how many times the recorder sends one write, and
// retryWait is the clock between two sends. An API server that
// restarts refuses connections for some seconds, and three sends 10
// seconds apart cover that without holding the queue for long. This
// is the interval of client-go's own recorder, which sends 12 times.
const (
	attempts  = 3
	retryWait = 10 * time.Second
)

// Recorder writes the Events of one component.
type Recorder struct {
	client    *apiclient.Client
	component string
	instance  string
	log       io.Writer

	queue   chan Event
	dropped atomic.Uint64

	// mu guards stopping, so that no post queues an Event after
	// Shutdown began and the goroutine may have read the queue for the
	// last time. stop closes when Shutdown begins, abandon when the
	// context of Shutdown ends, and done when the goroutine returns.
	mu          sync.Mutex
	stopping    bool
	stop        chan struct{}
	abandon     chan struct{}
	abandonOnce sync.Once
	done        chan struct{}

	// series is read and written only by the goroutine that writes
	// the queue.
	series *series
}

// Options are the optional parts of a Recorder.
type Options struct {
	// Instance is the reportingInstance of each Event: the name of the
	// pod, or of the node for a DaemonSet that runs on the host's
	// network. Empty means the host name, which is the pod's name in a
	// pod.
	Instance string

	// Log receives one line for each Event the recorder could not
	// write, and for the first Event it dropped. Nil means standard
	// error.
	Log io.Writer
}

// New starts a recorder that writes until ctx ends, or until Shutdown
// has written the queue. component is the
// reportingComponent of each Event, such as machine-operator.
func New(ctx context.Context, client *apiclient.Client, component string, options Options) *Recorder {
	instance := options.Instance
	if instance == "" {
		instance, _ = os.Hostname()
	}
	log := options.Log
	if log == nil {
		log = os.Stderr
	}
	r := &Recorder{
		client:    client.WithContext(ctx),
		component: component,
		instance:  instance,
		log:       log,
		queue:     make(chan Event, queueLength),
		series:    newSeries(),
		stop:      make(chan struct{}),
		abandon:   make(chan struct{}),
		done:      make(chan struct{}),
	}
	go r.run(ctx)
	return r
}

// Normal posts an Event about an expected transition or an action the
// component took. A nil recorder posts nothing.
func (r *Recorder) Normal(object ObjectReference, reason, message string) {
	r.post(object, TypeNormal, reason, message)
}

// Warning posts an Event about something a person may need to act on.
// A nil recorder posts nothing.
func (r *Recorder) Warning(object ObjectReference, reason, message string) {
	r.post(object, TypeWarning, reason, message)
}

// Dropped answers how many Events the recorder dropped because its
// queue was full.
func (r *Recorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// post builds an Event at the time of the call, and queues it.
func (r *Recorder) post(object ObjectReference, eventType, reason, message string) {
	if r == nil {
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	event := Event{
		APIVersion: "v1",
		Kind:       "Event",
		Metadata: Metadata{
			GenerateName: object.Name + ".",
			Namespace:    namespaceOf(object),
		},
		InvolvedObject:     object,
		Reason:             cut(reason, maxReason),
		Message:            cut(message, maxMessage),
		Type:               eventType,
		Source:             Source{Component: r.component, Host: r.instance},
		FirstTimestamp:     now,
		LastTimestamp:      now,
		Count:              1,
		ReportingComponent: r.component,
		ReportingInstance:  r.instance,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		r.logf("dropped the %s Event %s on the %s %s: the recorder has shut down", eventType, reason, object.Kind, object.Name)
		return
	}
	select {
	case r.queue <- event:
	default:
		if r.dropped.Add(1) == 1 {
			r.logf("dropped the %s Event %s on the %s %s: %d Events wait to be written", eventType, reason, object.Kind, object.Name, queueLength)
		}
	}
}

// ShutdownTimeout is the time an operator gives Shutdown. A queue of a
// few Events writes in milliseconds, and an API server that does not
// answer in 5 seconds will not answer before the pod's grace period
// ends. The rest of the default grace period of 30 seconds covers the
// release of a Lease, which kubernetes/election bounds at 16 seconds.
const ShutdownTimeout = 5 * time.Second

// Shutdown stops the recorder and writes the Events it holds. It
// follows client-go's event broadcaster: a later Normal or Warning
// drops its Event, each Event still queued is sent once, and a write
// that fails is logged and not sent again. An Event that waits to be
// sent again is given up at once. Shutdown answers nil when the queue
// is empty and the last request has returned, so the caller's next
// step, such as the release of a Lease, comes after every write of the
// recorder.
//
// Unlike client-go's Shutdown, it waits for that last request. When ctx
// ends first, Shutdown answers ctx's error at once, and a request can
// still be in flight. The recorder then sends nothing after that
// request returns, and logs how many Events it did not write.
func (r *Recorder) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.stopping {
		r.stopping = true
		close(r.stop)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
	}
	r.abandonOnce.Do(func() { close(r.abandon) })
	return fmt.Errorf("the Event recorder's last write had not returned: %w", ctx.Err())
}

// run writes the queue until ctx ends, or until Shutdown has emptied
// it.
func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	for {
		// A Shutdown takes the queue to drain, which stops when the
		// context of Shutdown ends. The select below picks at random
		// when the queue and stop are both ready, so stop is read
		// first.
		select {
		case <-r.stop:
			r.drain(ctx)
			return
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			r.drain(ctx)
			return
		case event := <-r.queue:
			r.write(ctx, event)
		}
	}
}

// drain sends each Event left in the queue once, after Shutdown. No
// post adds to the queue once Shutdown began, so an empty queue is the
// end.
func (r *Recorder) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.abandon:
			if left := len(r.queue); left > 0 {
				r.logf("the shutdown ran out of time, so these queued Events were not written: %d", left)
			}
			return
		default:
		}
		select {
		case event := <-r.queue:
			if err := r.writeOnce(event); err != nil {
				r.logFailure(event, err)
			}
		default:
			return
		}
	}
}

// write sends one Event, and sends it again after a failure, up to
// attempts times.
func (r *Recorder) write(ctx context.Context, event Event) {
	for attempt := 1; ; attempt++ {
		err := r.writeOnce(event)
		if err == nil {
			return
		}
		if attempt == attempts {
			r.logFailure(event, err)
			return
		}
		timer := time.NewTimer(retryWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.stop:
			timer.Stop()
			r.logFailure(event, err)
			return
		case <-timer.C:
		}
	}
}

// writeOnce patches the Event of a series that is still open, or
// creates a new Event.
func (r *Recorder) writeOnce(event Event) error {
	key := keyOf(event)
	collection := "/api/v1/namespaces/" + event.Metadata.Namespace + "/events"
	if held, ok := r.series.get(key); ok && event.LastTimestamp.Sub(held.last) < window {
		count := held.count + 1
		// Strings, counts, and times of this century always encode,
		// so neither Marshal below can fail.
		patch, _ := json.Marshal(map[string]any{"count": count, "lastTimestamp": event.LastTimestamp})
		err := r.client.Request(http.MethodPatch, collection+"/"+held.name, "application/merge-patch+json", patch, nil)
		if err == nil {
			r.series.put(key, entry{name: held.name, count: count, last: event.LastTimestamp})
			return nil
		}
		if !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
		// The TTL deleted the Event, so the series starts again.
		r.series.remove(key)
	}
	body, _ := json.Marshal(event)
	var created Event
	if err := r.client.RequestJSON(http.MethodPost, collection, body, &created); err != nil {
		return err
	}
	r.series.put(key, entry{name: created.Metadata.Name, count: 1, last: event.LastTimestamp})
	return nil
}

// logFailure logs the last failure of an Event that the recorder gives
// up on.
func (r *Recorder) logFailure(event Event, err error) {
	r.logf("writing the %s Event %s on the %s %s: %v", event.Type, event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name, err)
}

func (r *Recorder) logf(format string, args ...any) {
	fmt.Fprintf(r.log, r.component+": "+format+"\n", args...)
}
