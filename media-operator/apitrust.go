package main

// This file holds the trust store: the CA certificates media-api
// trusts when it calls a sibling. Each sibling publishes its CA in a
// ConfigMap the way this API publishes its own, so trust is public
// data read with get on three named objects and no Secret. The store
// watches the ConfigMaps because a rotation appends the coming CA to
// ca.crt in place, and the next connection must trust it with no
// restart.

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// ConfigMap is the core ConfigMap as this program reads and writes it.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func configMapsPath(namespace string) string { return podPrefix + namespace + "/configmaps" }

func GetConfigMap(c *Client, namespace, name string) (*ConfigMap, error) {
	configMap := &ConfigMap{}
	if err := c.RequestJSON(http.MethodGet, configMapsPath(namespace)+"/"+name, nil, configMap); err != nil {
		return nil, err
	}
	return configMap, nil
}

func CreateConfigMap(c *Client, configMap *ConfigMap) (*ConfigMap, error) {
	body, err := json.Marshal(configMap)
	if err != nil {
		return nil, err
	}
	created := &ConfigMap{}
	path := configMapsPath(configMap.Metadata.Namespace)
	if err := c.RequestJSON(http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// trustAnchor is one sibling's published file, every certificate in
// it, and the resourceVersion its watch resumes from.
type trustAnchor struct {
	certificates    []byte
	resourceVersion string
}

// A sibling this cluster has not deployed answers 404 to every watch.
// The wait between attempts starts here and doubles to the ceiling, so
// one absent sibling costs one request a minute rather than several a
// second. A watch that opens resets the wait, because the next failure
// is a new fault and not the same one.
const (
	watchBackoffStart = time.Second
	watchBackoffMax   = time.Minute
)

// trustStore holds the anchors, one per named ConfigMap, and the pool
// built from them that every request goroutine reads at dial time.
type trustStore struct {
	client    *Client
	namespace string
	names     []string

	// How long a dropped watch waits before it reads the object again.
	// It is a field so a test drives it in milliseconds.
	retry time.Duration

	// The bounds of the wait after a watch that would not open. They
	// are fields for the same reason.
	backoffStart time.Duration
	backoffMax   time.Duration

	// pause is the wait itself, and report is where a change of state
	// goes. Both are fields so a test drives the loop with no clock of
	// its own and reads what the loop said.
	pause  func(ctx context.Context, wait time.Duration) bool
	report func(line string)

	mu      sync.RWMutex
	anchors map[string]trustAnchor
	roots   *x509.CertPool
}

func newTrustStore(client *Client, namespace string, names ...string) *trustStore {
	return &trustStore{
		client:       client,
		namespace:    namespace,
		names:        names,
		retry:        watchRetryPause,
		backoffStart: watchBackoffStart,
		backoffMax:   watchBackoffMax,
		pause:        waiting,
		report:       func(line string) { fmt.Fprintln(os.Stderr, line) },
		anchors:      map[string]trustAnchor{},
		roots:        x509.NewCertPool(),
	}
}

// load reads every named ConfigMap once. A cluster may run one sibling
// and not the other, so an absent ConfigMap is not a failure; only an
// API server that will not answer is.
func (t *trustStore) load() error {
	for _, name := range t.names {
		if _, err := t.read(name); err != nil {
			return err
		}
	}
	return nil
}

func (t *trustStore) pool() *x509.CertPool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.roots
}

// watch follows each ConfigMap in a goroutine of its own, because the
// Role scopes the get and the watch by resourceNames and one watch
// cannot cover three named objects.
func (t *trustStore) watch(ctx context.Context) {
	for _, name := range t.names {
		go t.follow(ctx, name)
	}
}

// follow is the watch loop for one ConfigMap: stream from the last
// resourceVersion, and when the stream ends, pause, read the object
// again, and stream from there. The read is what recovers a version
// the API server no longer holds, so it runs only after a watch that
// opened; a watch the API server refused has no version to resume from
// and reading again would double the cost of a sibling that is absent.
//
// One line reports each change of state, the first refusal and the
// return, and nothing reports an attempt that failed the same way as
// the one before it.
func (t *trustStore) follow(ctx context.Context, name string) {
	resourceVersion := t.version(name)
	wait := t.backoffStart
	refusal := ""
	for ctx.Err() == nil {
		version, opened, reason := t.stream(ctx, name, resourceVersion)
		resourceVersion = version
		if !opened {
			if reason != refusal {
				t.report(fmt.Sprintf(
					"watching configmap %s: %s; media-api composes no stream through that API until it exists",
					name, reason))
				refusal = reason
			}
			if !t.pause(ctx, wait) {
				return
			}
			wait = min(wait*2, t.backoffMax)
			continue
		}
		if refusal != "" {
			t.report(fmt.Sprintf("watching configmap %s: the object is present and media-api trusts it", name))
			refusal = ""
		}
		wait = t.backoffStart
		if !t.pause(ctx, t.retry) {
			return
		}
		version, err := t.read(name)
		if err != nil {
			t.report(fmt.Sprintf("reading configmap %s to resume the watch: %v", name, err))
			continue
		}
		resourceVersion = version
	}
}

// stream opens one watch on the named object's own path, not on the
// collection with a field selector. RBAC reads resourceNames from the
// request path, so the Role that names these three ConfigMaps
// authorizes the object path and refuses a collection watch.
//
// It answers whether the watch opened, and the reason it did not. The
// caller owns the reporting, because only the caller knows whether the
// reason is the same one as last time.
func (t *trustStore) stream(ctx context.Context, name, resourceVersion string) (string, bool, string) {
	query := url.Values{
		"watch":               {"true"},
		"allowWatchBookmarks": {"true"},
		"resourceVersion":     {resourceVersion},
	}
	path := configMapsPath(t.namespace) + "/" + url.PathEscape(name) + "?" + query.Encode()
	resp, err := t.client.Do(http.MethodGet, path, nil)
	if err != nil {
		return resourceVersion, false, err.Error()
	}
	defer drain(resp.Body)
	stop := closeOnCancel(ctx, resp.Body)
	defer stop()

	// A sibling this cluster does not run answers 404, and a Role that
	// does not name this object answers 403. Both mean there is no
	// stream to read, and the reason reaches the caller's one line.
	if resp.StatusCode != http.StatusOK {
		return resourceVersion, false, resp.Status
	}
	return t.readEvents(name, resp, resourceVersion), true, ""
}

func (t *trustStore) readEvents(name string, resp *http.Response, resourceVersion string) string {
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string    `json:"type"`
			Object ConfigMap `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			return resourceVersion
		}
		// A 410 Gone arrives as an ERROR event. The read in the caller
		// answers it with a current resourceVersion.
		if event.Type == "ERROR" {
			return resourceVersion
		}
		if event.Object.Metadata.ResourceVersion != "" {
			resourceVersion = event.Object.Metadata.ResourceVersion
		}
		switch event.Type {
		case "ADDED", "MODIFIED":
			t.remember(name, &event.Object)
		case "DELETED":
			t.forget(name)
		}
	}
}

func (t *trustStore) read(name string) (string, error) {
	configMap, err := GetConfigMap(t.client, t.namespace, name)
	if errors.Is(err, ErrNotFound) {
		t.forget(name)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading configmap %s/%s: %w", t.namespace, name, err)
	}
	t.remember(name, configMap)
	return configMap.Metadata.ResourceVersion, nil
}

func (t *trustStore) remember(name string, configMap *ConfigMap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.anchors[name] = trustAnchor{
		certificates:    []byte(configMap.Data[apiCACertKey]),
		resourceVersion: configMap.Metadata.ResourceVersion,
	}
	t.rebuild()
}

func (t *trustStore) forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.anchors, name)
	t.rebuild()
}

// rebuild builds a new pool whole on every change and swaps it in, so
// a reader never sees a half-built one. The caller holds the write
// lock.
func (t *trustStore) rebuild() {
	roots := x509.NewCertPool()
	for _, name := range t.names {
		// A rotation appends the coming CA to the file, so every
		// certificate in it is an anchor and a leaf under either CA
		// verifies.
		roots.AppendCertsFromPEM(t.anchors[name].certificates)
	}
	t.roots = roots
}

func (t *trustStore) version(name string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.anchors[name].resourceVersion
}

// closeOnCancel closes the body when the context ends, because the
// client's Do carries no context and the decoder would otherwise
// block on a stream nobody reads.
func closeOnCancel(ctx context.Context, body io.Closer) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// waiting pauses for the retry and answers false when the context
// ended first, which is how each watch goroutine stops.
func waiting(ctx context.Context, pause time.Duration) bool {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
