// Package events posts Kubernetes Events about the objects that a
// liken component manages, so that `kubectl describe` shows what
// happened to an object in the last hour.
//
// A status condition tells automation what is true now. An Event tells
// a person what just happened. A log line records every attempt and
// detail. So a component posts one Event for each condition transition
// (SetCondition, or Transition after a status write) and one for each
// action that changes no condition, such as recreating a pod or
// requesting a reboot. It never posts a reading, a key press, or each
// retry of a retry loop.
//
// A component builds one Recorder in main and passes it down:
//
//	recorder := events.New(ctx, client, "observatory-operator", events.Options{})
//	recorder.Warning(object, "StepFailed", "The mount does not respond to CONNECT")
//	recorder.SetCondition(object, &status.Conditions, ready, conditions.False)
//
// The recorder writes core/v1 Events, which `kubectl describe` and
// `kubectl events` read. It writes them through apiclient and imports
// nothing from k8s.io, so a program that must not link client-go can
// use it.
//
// Writes are best effort, so a reconcile pass never waits for an Event
// and never fails because of one. Normal and Warning put the Event on a
// queue and return immediately, and one goroutine sends the queued
// Events to the API server. It tries each write up to three times and
// logs a line when the last try fails. When the queue is full, the
// recorder drops the Event and counts it in Dropped.
//
// When the same Event about the same object repeats within 10 minutes
// of its last repeat, the recorder patches count and lastTimestamp on
// the Event it already wrote. `kubectl describe` then prints one line
// such as "(x37 over 1h)" in place of 37 lines.
//
// The API server deletes an Event one hour after its last write
// (kube-apiserver's --event-ttl, which k3s does not change). A fact
// that must last longer belongs in a condition or a status field.
//
// The API server accepts an Event about a cluster-scoped object only
// in the namespace default or kube-system, so the recorder writes
// these Events in default. `kubectl events --for` finds them only with
// -n default or -A.
//
// A component needs the RBAC verbs create and patch on events in each
// namespace it posts to.
//
// When the context passed to New ends, the goroutine cancels its
// request and returns, and the Events still queued are lost. The
// cancelled request can still reach the API server after that. A
// component that needs its last write to return first, such as
// an elected operator that releases its Lease, passes New a context
// that outlives its last pass and calls Shutdown after that pass:
//
//	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
//	defer cancel()
//	if recorder.Shutdown(ctx) == nil {
//		lead.StepDown()
//	}
//
// A test serves eventstest.Events in front of its fake API server
// (from apiservertest), reads the written Events from it, and passes
// New t.Context().
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
// posts few Events for each object in an hour, so a full queue means
// that the API server stopped responding, and the recorder drops new
// Events instead of using more memory.
const queueLength = 256

// An API server that restarts refuses connections for some seconds.
// Three sends 10 seconds apart cover a restart without blocking the
// queue for long. client-go's event recorder also waits 10 seconds
// between sends.
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

	// mu guards stopping. Once Shutdown sets it, post queues no
	// more Events, because the goroutine may already have read the
	// queue for the last time. stop closes when Shutdown starts,
	// abandon when the context passed to Shutdown ends, and done
	// when the goroutine returns.
	mu          sync.Mutex
	stopping    bool
	stop        chan struct{}
	abandon     chan struct{}
	abandonOnce sync.Once
	done        chan struct{}

	// Only the goroutine that sends the queued Events reads and
	// writes series.
	series *series
}

// Options are the optional parts of a Recorder.
type Options struct {
	// Instance is the reportingInstance of each Event: the name of
	// the pod, or the name of the node for a DaemonSet pod that
	// uses the host's network. When empty, the recorder uses the
	// host name, which is the pod's name inside a pod.
	Instance string

	// Log receives a line for each Event that the recorder fails to
	// write, for the first Event that it drops because the queue is
	// full, for each Event posted after Shutdown, and for the count
	// of Events that Shutdown did not write. When nil, the recorder
	// writes to standard error.
	Log io.Writer
}

// New starts a recorder that writes Events until ctx ends or until
// Shutdown has sent the queued Events. component is the reportingComponent
// of each Event, such as machine-operator.
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

// Normal posts an Event about an expected transition or an action that
// the component took. A nil recorder posts nothing.
func (r *Recorder) Normal(object ObjectReference, reason, message string) {
	r.post(object, TypeNormal, reason, message)
}

// Warning posts an Event about something that a person may need to act
// on. A nil recorder posts nothing.
func (r *Recorder) Warning(object ObjectReference, reason, message string) {
	r.post(object, TypeWarning, reason, message)
}

// Dropped returns how many Events the recorder dropped because its
// queue was full.
func (r *Recorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

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
		r.logf("dropped the %s Event %s on the %s: the recorder has shut down", eventType, reason, describe(object))
		return
	}
	select {
	case r.queue <- event:
	default:
		if r.dropped.Add(1) == 1 {
			r.logf("dropped the %s Event %s on the %s: %d Events wait to be written", eventType, reason, describe(object), queueLength)
		}
	}
}

// ShutdownTimeout is the timeout of the context that an operator passes
// to Shutdown. A few queued Events normally write in milliseconds, so
// an API server that has not responded in 5 seconds is unlikely to
// respond before the pod's grace period ends. The remaining 25 seconds
// of the default 30-second grace period cover the release of the
// Lease, which kubernetes/election limits to 16 seconds.
const ShutdownTimeout = 5 * time.Second

// Shutdown stops the recorder and sends each Event still in its queue
// once, with no retry. A write that fails is logged. An Event that is
// waiting for a retry is logged and dropped, and a Normal or Warning
// call after Shutdown drops its Event. Shutdown returns nil after the
// queue is empty and the last request has returned, so the caller's
// next step, such as releasing a Lease, happens after every write.
//
// When ctx ends first, Shutdown returns ctx's error at once, and a
// request can still be in flight. The recorder sends nothing after that
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

// run sends the queued Events to the API server until ctx ends or until
// Shutdown has emptied the queue.
func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	for {
		// When the queue and stop are both ready, the select
		// below picks one at random. Checking stop first makes
		// Shutdown start draining instead of writing more
		// Events with retries.
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

// drain sends each Event left in the queue once. post queues nothing
// after Shutdown starts, so drain returns when the queue is empty.
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
			if err := r.writeOnce(event); err != nil && !namespaceGone(err) {
				r.logFailure(event, err)
			}
		default:
			return
		}
	}
}

func (r *Recorder) write(ctx context.Context, event Event) {
	for attempt := 1; ; attempt++ {
		err := r.writeOnce(event)
		if err == nil || namespaceGone(err) {
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

// writeOnce patches the earlier Event of a repeat within the window, or
// creates a new Event.
func (r *Recorder) writeOnce(event Event) error {
	key := keyOf(event)
	collection := "/api/v1/namespaces/" + event.Metadata.Namespace + "/events"
	if earlier, ok := r.series.get(key); ok && event.LastTimestamp.Sub(earlier.last) < window {
		count := earlier.count + 1
		// json.Marshal fails on a time only outside the years 0
		// to 9999, so neither Marshal below can fail.
		patch, _ := json.Marshal(map[string]any{"count": count, "lastTimestamp": event.LastTimestamp})
		err := r.client.Request(http.MethodPatch, collection+"/"+earlier.name, "application/merge-patch+json", patch, nil)
		if err == nil {
			r.series.put(key, entry{name: earlier.name, count: count, last: event.LastTimestamp})
			return nil
		}
		if !errors.Is(err, apiclient.ErrNotFound) {
			return err
		}
		// The API server deleted the Event after its TTL, so
		// create a new one.
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

// namespaceGone reports whether a write failed because the Event's namespace
// does not exist or the API server is deleting it. The recorder then
// drops the Event with no retry and no log line, because nobody could
// read it. While a namespace is deleted, the API server deletes its
// objects one at a time, and an operator posts a burst of these Events.
// writeOnce handles a 404 on a patch itself, so a 404 here is from a
// create, and means the namespace is gone.
func namespaceGone(err error) bool {
	return errors.Is(err, apiclient.ErrNamespaceTerminating) || errors.Is(err, apiclient.ErrNotFound)
}

func (r *Recorder) logFailure(event Event, err error) {
	r.logf("writing the %s Event %s on the %s: %v", event.Type, event.Reason, describe(event.InvolvedObject), err)
}

// describe returns an object's kind, namespace, and name for a log
// line.
func describe(object ObjectReference) string {
	if object.Namespace == "" {
		return object.Kind + " " + object.Name
	}
	return object.Kind + " " + object.Namespace + "/" + object.Name
}

func (r *Recorder) logf(format string, args ...any) {
	fmt.Fprintf(r.log, r.component+": "+format+"\n", args...)
}
