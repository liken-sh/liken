// Package eventstest fakes the API server's core/v1 events collection.
// A test serves it through apiservertest, beside the test's own fake,
// and reads back the Events that events.Recorder wrote.
package eventstest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/liken-sh/liken/kubernetes/events"
)

var eventPath = regexp.MustCompile(`^/api/v1/namespaces/[^/]+/events(/[^/]+)?$`)

// Events stores the Events that a test's fake API server receives. It
// handles creates and merge patches like the API server does: a create
// without a name gets one from generateName, a patch of an Event that
// does not exist returns 404, and an Event about a cluster-scoped
// object is rejected outside the default and kube-system namespaces.
// The zero value is an empty store that is ready to serve.
type Events struct {
	mu      sync.Mutex
	stored  []events.Event
	created int
	// failNext is the number of writes that still return 503.
	failNext int
	// namespaceStatus maps a namespace to the status that every create
	// in it returns.
	namespaceStatus map[string]int
	mux             *http.ServeMux
}

// Around returns a handler that serves Event requests itself and
// passes every other request to next, the test's own fake:
//
//	recorded := &eventstest.Events{}
//	server := apiservertest.Start(t, recorded.Around(fake))
func (e *Events) Around(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if eventPath.MatchString(r.URL.Path) {
			e.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP serves a create or a patch of an Event.
func (e *Events) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	if e.mux == nil {
		e.mux = http.NewServeMux()
		e.mux.HandleFunc("POST /api/v1/namespaces/{namespace}/events", e.create)
		e.mux.HandleFunc("PATCH /api/v1/namespaces/{namespace}/events/{name}", e.patch)
	}
	mux := e.mux
	e.mu.Unlock()
	mux.ServeHTTP(w, r)
}

// Refuse makes the next count writes return 503, like an API server
// that is restarting.
func (e *Events) Refuse(count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failNext = count
}

// Terminate makes every create in namespace return the 403 that the
// API server returns while it deletes the namespace.
func (e *Events) Terminate(namespace string) { e.setNamespaceStatus(namespace, http.StatusForbidden) }

// DeleteNamespace makes every create in namespace return 404, like the
// API server does after it deletes the namespace.
func (e *Events) DeleteNamespace(namespace string) {
	e.setNamespaceStatus(namespace, http.StatusNotFound)
}

func (e *Events) setNamespaceStatus(namespace string, status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.namespaceStatus == nil {
		e.namespaceStatus = map[string]int{}
	}
	e.namespaceStatus[namespace] = status
}

// terminating is the body of the API server's 403 for a create in a
// namespace that it is deleting. apiclient reads only the cause
// NamespaceTerminating, so the fake leaves out the message.
const terminating = `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","details":{"causes":[{"reason":"NamespaceTerminating"}]},"code":403}`

// Expire deletes every Event, like the API server does an hour after
// an Event's last write.
func (e *Events) Expire() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stored = nil
}

// List returns every stored Event in creation order.
func (e *Events) List() []events.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.stored)
}

// About returns the Events whose involved object has this kind and
// name, in creation order.
func (e *Events) About(kind, name string) []events.Event {
	var out []events.Event
	for _, event := range e.List() {
		if event.InvolvedObject.Kind == kind && event.InvolvedObject.Name == name {
			out = append(out, event)
		}
	}
	return out
}

// failIfRestarting writes a 503 while Refuse has writes left to fail.
// The caller holds e.mu.
func (e *Events) failIfRestarting(w http.ResponseWriter) bool {
	if e.failNext == 0 {
		return false
	}
	e.failNext--
	http.Error(w, "the API server is restarting", http.StatusServiceUnavailable)
	return true
}

func (e *Events) create(w http.ResponseWriter, r *http.Request) {
	var event events.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	namespace := r.PathValue("namespace")
	involved := event.InvolvedObject.Namespace
	switch {
	case event.Metadata.Namespace != namespace:
		http.Error(w, "the namespace of the Event does not match the namespace of the request", http.StatusBadRequest)
		return
	case involved == "" && namespace != "default" && namespace != "kube-system":
		http.Error(w, "an Event about a cluster-scoped object must be in default or kube-system", http.StatusUnprocessableEntity)
		return
	case involved != "" && involved != namespace:
		http.Error(w, "the involved object's namespace does not match the Event's", http.StatusUnprocessableEntity)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.namespaceStatus[namespace] {
	case http.StatusForbidden:
		http.Error(w, terminating, http.StatusForbidden)
		return
	case http.StatusNotFound:
		http.Error(w, "the namespace does not exist", http.StatusNotFound)
		return
	}
	if e.failIfRestarting(w) {
		return
	}
	e.created++
	if event.Metadata.Name == "" {
		event.Metadata.Name = event.Metadata.GenerateName + fmt.Sprintf("%05x", e.created)
	}
	event.Metadata.ResourceVersion = strconv.Itoa(e.created)
	e.stored = append(e.stored, event)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(event)
}

func (e *Events) patch(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || r.Header.Get("Content-Type") != "application/merge-patch+json" {
		http.Error(w, "the fake accepts only a JSON merge patch", http.StatusUnsupportedMediaType)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failIfRestarting(w) {
		return
	}
	at := slices.IndexFunc(e.stored, func(stored events.Event) bool {
		return stored.Metadata.Namespace == r.PathValue("namespace") && stored.Metadata.Name == r.PathValue("name")
	})
	if at < 0 {
		http.Error(w, "the Event does not exist", http.StatusNotFound)
		return
	}
	var document map[string]any
	current, _ := json.Marshal(e.stored[at])
	_ = json.Unmarshal(current, &document)
	merged, _ := json.Marshal(mergePatch(document, patch))
	var event events.Event
	if err := json.Unmarshal(merged, &event); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	e.stored[at] = event
	_ = json.NewEncoder(w).Encode(event)
}

// mergePatch applies a JSON merge patch (RFC 7386) to document. A null
// removes the field, an object merges into the existing object, and any
// other value replaces the field.
func mergePatch(document, patch map[string]any) map[string]any {
	for key, value := range patch {
		inner, isObject := value.(map[string]any)
		existing, existingIsObject := document[key].(map[string]any)
		switch {
		case value == nil:
			delete(document, key)
		case isObject && existingIsObject:
			document[key] = mergePatch(existing, inner)
		default:
			document[key] = value
		}
	}
	return document
}
