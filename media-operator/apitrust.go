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

// trustStore holds the anchors, one per named ConfigMap, and the pool
// built from them that every request goroutine reads at dial time.
type trustStore struct {
	client    *Client
	namespace string
	names     []string

	// How long a dropped watch waits before it reads the object again.
	// It is a field so a test drives it in milliseconds.
	retry time.Duration

	mu      sync.RWMutex
	anchors map[string]trustAnchor
	roots   *x509.CertPool
}

func newTrustStore(client *Client, namespace string, names ...string) *trustStore {
	return &trustStore{
		client:    client,
		namespace: namespace,
		names:     names,
		retry:     watchRetryPause,
		anchors:   map[string]trustAnchor{},
		roots:     x509.NewCertPool(),
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
// the API server no longer holds.
func (t *trustStore) follow(ctx context.Context, name string) {
	resourceVersion := t.version(name)
	for ctx.Err() == nil {
		resourceVersion = t.stream(ctx, name, resourceVersion)
		if !waiting(ctx, t.retry) {
			return
		}
		version, err := t.read(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading configmap %s to resume the watch: %v\n", name, err)
			continue
		}
		resourceVersion = version
	}
}

// stream opens one watch on the named object's own path, not on the
// collection with a field selector. RBAC reads resourceNames from the
// request path, so the Role that names these three ConfigMaps
// authorizes the object path and refuses a collection watch.
func (t *trustStore) stream(ctx context.Context, name, resourceVersion string) string {
	query := url.Values{
		"watch":               {"true"},
		"allowWatchBookmarks": {"true"},
		"resourceVersion":     {resourceVersion},
	}
	path := configMapsPath(t.namespace) + "/" + url.PathEscape(name) + "?" + query.Encode()
	resp, err := t.client.Do(http.MethodGet, path, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching configmap %s: %v\n", name, err)
		return resourceVersion
	}
	defer drain(resp.Body)
	stop := closeOnCancel(ctx, resp.Body)
	defer stop()

	// A status other than 200 is reported, because a watch this
	// program cannot open is a grant it does not hold, and without
	// the line the loop would read the object on a timer forever
	// and nobody would learn why.
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "watching configmap %s: %s\n", name, resp.Status)
		return resourceVersion
	}
	return t.readEvents(name, resp, resourceVersion)
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
