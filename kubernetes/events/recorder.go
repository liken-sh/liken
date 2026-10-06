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
// A test serves eventstest.Events in front of its fake API server,
// through apiservertest, and reads what the recorder wrote from it. The recorder's goroutine
// ends with the context New takes, so a test passes t.Context().
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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

// New starts a recorder that writes until ctx ends. component is the
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
	select {
	case r.queue <- event:
	default:
		if r.dropped.Add(1) == 1 {
			r.logf("dropped the %s Event %s on the %s %s: %d Events wait to be written", eventType, reason, object.Kind, object.Name, queueLength)
		}
	}
}

// run writes the queue until ctx ends.
func (r *Recorder) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-r.queue:
			r.write(ctx, event)
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
			r.logf("writing the %s Event %s on the %s %s: %v", event.Type, event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name, err)
			return
		}
		timer := time.NewTimer(retryWait)
		select {
		case <-ctx.Done():
			timer.Stop()
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

func (r *Recorder) logf(format string, args ...any) {
	fmt.Fprintf(r.log, r.component+": "+format+"\n", args...)
}
