package main

// The webhook is on the operator. Radarr, Sonarr, and Jellyfin
// post to one address with a path per Library, and the operator reads
// the changed path out of the payload and creates a scan Job for that
// one folder. The scanner answers no webhook of its own any more,
// because a scan is a Job that runs and exits, and an address must
// stand between runs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// The variables the Deployment states: the namespace the operator's own
// Service is in, which is what the reported address names, and the port
// the operator listens on.
const (
	operatorNamespaceVariable = "OPERATOR_NAMESPACE"
	webhookPortVariable       = "WEBHOOK_PORT"
	defaultWebhookPort        = "8080"
)

// The Service over the operator, the path each Library answers on, and
// the scheme and DNS suffix the reported address is built from. A name
// of this form resolves from any pod in the cluster, which is where the
// media servers that send these webhooks run.
const (
	operatorServiceName = "library-operator"
	webhookPathPrefix   = "/webhook/"
	webhookScheme       = "http://"
	clusterDNSSuffix    = ".svc"
)

// How long the server waits for a sender's headers, so a
// connection that opens and says nothing cannot hold a slot.
const webhookHeaderTimeout = 10 * time.Second

// How many paths one Library holds before the operator stops
// keeping them apart. Past the limit one full walk covers every path at
// once, and a sender that floods the endpoint costs one walk rather
// than unbounded memory.
const heldPathLimit = 64

// WebhookURL is the address a person gives to Radarr, Sonarr, or
// Jellyfin. It names the operator's own Service and the Library, and
// never a pod, so it is the same address for the whole life of the
// Library.
func webhookURL(operatorNamespace, namespace, name string) string {
	return webhookScheme + operatorServiceName + "." + operatorNamespace + clusterDNSSuffix +
		webhookPathPrefix + namespace + "/" + name
}

// The paths the operator holds for each Library until it can
// create their Jobs. A restart loses them, which the next full walk
// covers.
type heldPaths struct {
	mutex sync.Mutex
	paths map[string]map[string]bool
	wake  chan<- struct{}
}

func newHeldPaths(wake chan<- struct{}) *heldPaths {
	return &heldPaths{paths: map[string]map[string]bool{}, wake: wake}
}

// One path is held for one Library and the loop is woken, so the
// Job is created on the next pass rather than on the next tick.
func (h *heldPaths) hold(namespace, name, path string) {
	key := libraryKey(namespace, name)
	h.mutex.Lock()
	held, standing := h.paths[key]
	if !standing {
		held = map[string]bool{}
		h.paths[key] = held
	}
	// A full walk already covers every path, so nothing is held beside
	// one. Past the limit the whole set collapses to that walk, and a
	// sender that floods the endpoint costs one walk.
	if held[""] {
		h.mutex.Unlock()
		return
	}
	// A full walk held after a folder path replaces the set, so a Library
	// never stands two scan Jobs for one claim that admits one writer.
	if path == "" || len(held) >= heldPathLimit {
		h.paths[key] = map[string]bool{"": true}
	} else {
		held[path] = true
	}
	h.mutex.Unlock()
	poke(h.wake)
}

// The paths one Library holds now, sorted, so a pass creates
// their Jobs in one order.
func (h *heldPaths) held(namespace, name string) []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return slices.Sorted(maps.Keys(h.paths[libraryKey(namespace, name)]))
}

// A path is released once its Job exists.
func (h *heldPaths) release(namespace, name, path string) {
	key := libraryKey(namespace, name)
	h.mutex.Lock()
	defer h.mutex.Unlock()
	delete(h.paths[key], path)
	if len(h.paths[key]) == 0 {
		delete(h.paths, key)
	}
}

// Paths held for a Library the collection no longer holds are
// dropped, the rule the report desk follows, so a webhook for a deleted
// Library never creates a Job.
func (h *heldPaths) retain(live map[string]bool) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for key := range h.paths {
		if !live[key] {
			delete(h.paths, key)
		}
	}
}

// The endpoint the media servers post to. It reads the changed
// path out of the payload, holds it for the Library the URL names, and
// answers no-content. A payload it cannot read a path from holds the
// empty path, which is a full walk, so a webhook is never worse than
// the schedule.
func (o *operator) webhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		namespace, name, ok := parseWebhookPath(request.URL.Path)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(request.Body, webhookBodyLimit))
		o.paths.hold(namespace, name, extractWebhookPath(body))
		w.WriteHeader(http.StatusNoContent)
	})
}

// The Library one request's path names. A path with anything but
// a namespace and a name under the prefix names no Library.
func parseWebhookPath(urlPath string) (namespace, name string, ok bool) {
	if !strings.HasPrefix(urlPath, webhookPathPrefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(urlPath, webhookPathPrefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// The server runs for the life of the operator and stops with
// it. A failure to listen ends the process, because an operator that
// reports a webhook address nothing answers is worse than one that
// refuses to start.
func (o *operator) serveWebhooks(stopped context.Context, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           o.webhookHandler(),
		ReadHeaderTimeout: webhookHeaderTimeout,
	}
	go func() {
		<-stopped.Done()
		// The shutdown takes a context of its own, because the one that
		// ended is the signal to shut down.
		ending, done := context.WithTimeout(context.Background(), passTimeout)
		defer done()
		if err := server.Shutdown(ending); err != nil {
			fmt.Fprintf(os.Stderr, "shutting the webhook server down: %v\n", err)
		}
	}()
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
