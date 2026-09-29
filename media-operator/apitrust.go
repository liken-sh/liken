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

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/dynamic"
)

// ConfigMap is the core ConfigMap as this program reads and writes it.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

func configMapsPath(namespace string) string { return podPrefix + namespace + "/configmaps" }

func GetConfigMap(c *apiclient.Client, namespace, name string) (*ConfigMap, error) {
	configMap := &ConfigMap{}
	if err := c.RequestJSON(http.MethodGet, configMapsPath(namespace)+"/"+name, nil, configMap); err != nil {
		return nil, err
	}
	return configMap, nil
}

func CreateConfigMap(c *apiclient.Client, configMap *ConfigMap) (*ConfigMap, error) {
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
	namespace string
	names     []string

	// report is where a change of state goes. It is a field so a test
	// reads what the store said.
	report func(line string)

	mu      sync.RWMutex
	anchors map[string]trustAnchor
	absent  map[string]bool
	roots   *x509.CertPool
}

func newTrustStore(namespace string, names ...string) *trustStore {
	return &trustStore{
		namespace: namespace,
		names:     names,
		report:    func(line string) { fmt.Fprintln(os.Stderr, line) },
		anchors:   map[string]trustAnchor{},
		absent:    map[string]bool{},
		roots:     x509.NewCertPool(),
	}
}

func (t *trustStore) pool() *x509.CertPool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.roots
}

// follow watches each ConfigMap in a goroutine of its own until the
// context ends, because the Role scopes the list and the watch by
// resourceNames, and a field selector names one object, so one watch
// cannot cover three. It answers one channel per ConfigMap, which
// closes when the store has taken that ConfigMap's first read. A
// cluster may run one sibling and not the other, so an absent
// ConfigMap is a read like any other.
func (t *trustStore) follow(ctx context.Context, watcher dynamic.Interface) []<-chan struct{} {
	read := make([]<-chan struct{}, 0, len(t.names))
	for _, name := range t.names {
		synced := make(chan struct{})
		read = append(read, synced)
		anchor := informer.One{Resource: configMapResource, Namespace: t.namespace, Name: name,
			What: "configmap " + t.namespace + "/" + name}
		informer.WatchOne(ctx, watcher, anchor, func(configMap *ConfigMap) {
			if configMap == nil {
				t.forget(name)
				return
			}
			t.remember(name, configMap)
		}, func() { close(synced) })
	}
	return read
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
