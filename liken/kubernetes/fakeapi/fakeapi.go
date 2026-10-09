// Package fakeapi is a small Kubernetes API server for the operators'
// tests. It holds collections of objects by their URL path, and it
// answers the requests an operator's pass and its watches send: a
// list, a streaming list, a read of one object by name, a create, an
// update, and a watch that receives each write. It records every
// request except the watches, so a test can count what a pass sends.
//
// A test serves it with apiservertest.Start, over in-memory
// connections, so the test can run in a synctest bubble and wait out
// the reflector's backoff on the fake clock.
//
// It is not a model of the API server. It ignores selectors, answers
// every list with every object of the collection, and checks no
// resourceVersion on an update. A test that needs one of those
// behaviors scripts its own handler.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Collection is one collection's kind and its objects. The kind goes
// in the bookmark that ends a streaming list.
type Collection struct {
	APIVersion string
	Kind       string
	Items      []map[string]any
}

// Server holds the collections by URL path.
type Server struct {
	mu          sync.Mutex
	collections map[string]*Collection
	requests    []string
	version     int
	watchers    map[string][]chan string

	// held, while it is true, queues each write's event in queued
	// instead of sending it, so a test can run a pass whose copies lag
	// a write.
	held   bool
	queued []queuedEvent
}

type queuedEvent struct {
	collection string
	line       string
}

// Hold queues the event of each write from now on, instead of sending
// it to the watchers.
func (s *Server) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = true
}

// Release sends every queued event, in order, and sends each later
// write's event at once again.
func (s *Server) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = false
	for _, e := range s.queued {
		s.send(e.collection, e.line)
	}
	s.queued = nil
}

// New returns a server that holds the given collections.
func New(collections map[string]*Collection) *Server {
	return &Server{collections: collections, version: 10, watchers: map[string][]chan string{}}
}

// Object builds an object with the metadata every kind carries.
func Object(apiVersion, kind, namespace, name string, fields map[string]any) map[string]any {
	metadata := map[string]any{"name": name, "resourceVersion": "5", "uid": "uid-" + name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": metadata}
	for k, v := range fields {
		o[k] = v
	}
	return o
}

// Requests answers each request since the last Forget, as
// "METHOD path", in the order they arrived.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Forget clears the record of requests.
func (s *Server) Forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

// ResourceVersion answers the resourceVersion of one object, or "" when
// the collection holds no object of that name.
func (s *Server) ResourceVersion(collection, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.collections[collection].Items {
		if metadata := item["metadata"].(map[string]any); metadata["name"] == name {
			version, _ := metadata["resourceVersion"].(string)
			return version
		}
	}
	return ""
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("watch") == "true" {
		s.stream(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	path := strings.TrimSuffix(r.URL.Path, "/status")
	if c, ok := s.collections[path]; ok {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": c.APIVersion, "kind": c.Kind + "List",
				"metadata": map[string]any{"resourceVersion": strconv.Itoa(s.version)},
				"items":    c.Items,
			})
		case http.MethodPost:
			var created map[string]any
			_ = json.NewDecoder(r.Body).Decode(&created)
			s.store(path, "ADDED", created)
			c.Items = append(c.Items, created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(created)
		}
		return
	}
	collection, name := path[:strings.LastIndex(path, "/")], path[strings.LastIndex(path, "/")+1:]
	if c, ok := s.collections[collection]; ok {
		for i, item := range c.Items {
			if item["metadata"].(map[string]any)["name"] != name {
				continue
			}
			if r.Method == http.MethodDelete {
				s.store(collection, "DELETED", item)
				c.Items = append(c.Items[:i], c.Items[i+1:]...)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			if r.Method == http.MethodPut {
				var written map[string]any
				_ = json.NewDecoder(r.Body).Decode(&written)
				s.store(collection, "MODIFIED", written)
				c.Items[i] = written
				item = written
			}
			_ = json.NewEncoder(w).Encode(item)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`))
}

// store gives a written object the next resourceVersion and sends it to
// the collection's watchers. The caller holds mu.
func (s *Server) store(collection, event string, object map[string]any) {
	s.version++
	object["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(s.version)
	line, _ := json.Marshal(map[string]any{"type": event, "object": object})
	if s.held {
		s.queued = append(s.queued, queuedEvent{collection, string(line)})
		return
	}
	s.send(collection, string(line))
}

// send gives one event line to each watcher of a collection. The
// caller holds mu.
func (s *Server) send(collection, line string) {
	for _, watcher := range s.watchers[collection] {
		watcher <- line
	}
}

// stream answers a streaming list: an ADDED event for each object of
// the collection, the bookmark that ends the initial events, and then
// each write to the collection until the watcher hangs up.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	c, ok := s.collections[r.URL.Path]
	if !ok {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	items := slices.Clone(c.Items)
	version := s.version
	writes := make(chan string, 64)
	s.watchers[r.URL.Path] = append(s.watchers[r.URL.Path], writes)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.watchers[r.URL.Path] = slices.DeleteFunc(s.watchers[r.URL.Path], func(c chan string) bool { return c == writes })
	}()
	for _, item := range items {
		object, _ := json.Marshal(item)
		fmt.Fprintf(w, `{"type":"ADDED","object":%s}`+"\n", object)
	}
	fmt.Fprintf(w, `{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`+"\n",
		c.APIVersion, c.Kind, strconv.Itoa(version))
	w.(http.Flusher).Flush()
	for {
		select {
		case line := <-writes:
			fmt.Fprintln(w, line)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}
