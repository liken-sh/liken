package main

// This file holds the trust store: the CA certificates media-api
// trusts when it calls a sibling. Each sibling publishes its CA in a
// ConfigMap the way this API publishes its own, so trust is public
// data read with list and watch on three named objects and no Secret.
// The store watches the ConfigMaps because a rotation appends the
// coming CA to ca.crt in place, and the next connection must trust it
// with no restart.

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
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

// trustAnchor is one sibling's published file: every certificate in
// it.
type trustAnchor struct {
	certificates []byte
}

// trustStore holds the anchors, one per named ConfigMap, and the pool
// built from them that every request goroutine reads at dial time.
type trustStore struct {
	client    *Client
	namespace string
	names     []string

	// How long an ended watch waits before it opens again, and the
	// bounds of the wait after a refused one. They are fields so a
	// test drives the watch in milliseconds.
	retry        time.Duration
	backoffStart time.Duration
	backoffMax   time.Duration

	// pause is the wait itself, and report is where a change of state
	// goes. Both are fields so a test drives the loop with no clock of
	// its own and reads what the loop said.
	pause  func(ctx context.Context, wait time.Duration) bool
	report func(line string)

	mu      sync.RWMutex
	anchors map[string]trustAnchor
	absent  map[string]bool
	roots   *x509.CertPool

	// listed holds, per name, the resourceVersion of the list that
	// load read. Each watch starts from it, not from the ConfigMap's
	// own version, which can be older than the API server's watch
	// window.
	listed map[string]string
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
		absent:       map[string]bool{},
		roots:        x509.NewCertPool(),
		listed:       map[string]string{},
	}
}

// load reads every named ConfigMap once. A cluster may run one sibling
// and not the other, so an absent ConfigMap is not a failure; only an
// API server that will not answer is.
func (t *trustStore) load() error {
	for _, name := range t.names {
		version, err := t.follower(name).read()
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.listed[name] = version
		t.mu.Unlock()
	}
	return nil
}

func (t *trustStore) pool() *x509.CertPool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.roots
}

// watch follows each ConfigMap in a goroutine of its own, because the
// Role scopes the list and the watch by resourceNames, and a field
// selector names one object, so one watch cannot cover three.
func (t *trustStore) watch(ctx context.Context) {
	for _, name := range t.names {
		go t.follow(ctx, name)
	}
}

// follow watches one ConfigMap from the version of the list that
// load read.
func (t *trustStore) follow(ctx context.Context, name string) {
	t.follower(name).follow(ctx, t.version(name))
}

func (t *trustStore) follower(name string) *namedWatch {
	watch := newNamedWatch(t.client, t.namespace, "configmaps", name,
		func(object json.RawMessage) error {
			var configMap ConfigMap
			if err := json.Unmarshal(object, &configMap); err != nil {
				return err
			}
			t.remember(name, &configMap)
			return nil
		},
		func() { t.forget(name) })
	watch.retry = t.retry
	watch.backoffStart = t.backoffStart
	watch.backoffMax = t.backoffMax
	watch.pause = t.pause
	watch.report = t.report
	return watch
}

// remember takes up a ConfigMap's anchors. A ConfigMap that returns
// after it was absent reports one line, because a person who read the
// absence line reads when it ended.
func (t *trustStore) remember(name string, configMap *ConfigMap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.anchors[name] = trustAnchor{certificates: []byte(configMap.Data[apiCACertKey])}
	t.rebuild()
	if t.absent[name] {
		delete(t.absent, name)
		t.report(fmt.Sprintf("configmap %s/%s is present and media-api trusts it", t.namespace, name))
	}
}

// forget drops a ConfigMap's anchors. The first absence reports one
// line, and a repeated one reports nothing.
func (t *trustStore) forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.anchors, name)
	t.rebuild()
	if !t.absent[name] {
		t.absent[name] = true
		t.report(fmt.Sprintf(
			"configmap %s/%s is absent; media-api composes no stream through that API until it exists",
			t.namespace, name))
	}
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
	return t.listed[name]
}
