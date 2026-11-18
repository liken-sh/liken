package main

// The trust store holds the CA certificates that media-api trusts when
// it calls the display API or the audio API. Each of those siblings
// publishes its CA in a ConfigMap, so media-api needs no access to their
// Secrets. The store watches the ConfigMaps because a CA rotation
// appends the new CA to ca.crt, and the next connection must trust it
// without a restart.

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

// trustAnchor is the contents of one sibling's ca.crt, which can hold
// several PEM certificates.
type trustAnchor struct {
	certificates []byte
}

// trustStore holds the certificates of each named ConfigMap and the
// pool built from them, which each request reads when it dials.
type trustStore struct {
	namespace string
	names     []string

	// report receives a line when a ConfigMap disappears or comes back.
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

// follow starts one watch for each ConfigMap, because the Role grants
// list and watch only by resourceNames and a field selector matches only
// one name. Each returned channel closes after the first read of its
// ConfigMap, whether or not the ConfigMap exists, because a cluster can
// run one sibling and not the other.
func (t *trustStore) follow(ctx context.Context, watcher dynamic.Interface) []<-chan struct{} {
	read := make([]<-chan struct{}, 0, len(t.names))
	for _, name := range t.names {
		synced := make(chan struct{})
		read = append(read, synced)
		anchor := informer.One{Resource: configMapResource, Namespace: t.namespace, Name: name,
			What: "ConfigMap " + t.namespace + "/" + name}
		informer.WatchOne(ctx, watcher, anchor, func(configMap *ConfigMap) {
			if configMap == nil {
				t.removeAnchors(name)
				return
			}
			t.setAnchors(name, configMap)
		}, func() { close(synced) })
	}
	return read
}

// setAnchors stores the CA certificates of a ConfigMap and rebuilds the
// pool. It reports a ConfigMap that comes back after it was absent.
func (t *trustStore) setAnchors(name string, configMap *ConfigMap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.anchors[name] = trustAnchor{certificates: []byte(configMap.Data[apiCACertKey])}
	t.rebuild()
	if t.absent[name] {
		delete(t.absent, name)
		t.report(fmt.Sprintf("ConfigMap %s/%s is present again, and media-api trusts its CA certificates", t.namespace, name))
	}
}

// removeAnchors removes the CA certificates of a ConfigMap and rebuilds
// the pool. It reports the first absence and nothing for a repeat.
func (t *trustStore) removeAnchors(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.anchors, name)
	t.rebuild()
	if !t.absent[name] {
		t.absent[name] = true
		t.report(fmt.Sprintf(
			"ConfigMap %s/%s does not exist, so media-api does not trust its CA, and requests to the API whose certificate that CA signs fail the TLS check until the ConfigMap exists",
			t.namespace, name))
	}
}

// rebuild builds a new pool and swaps it in, so a reader never sees a
// half-built pool. The caller holds the write lock.
func (t *trustStore) rebuild() {
	roots := x509.NewCertPool()
	for _, name := range t.names {
		// During a rotation ca.crt holds the old and the new CA, and
		// both must verify.
		roots.AppendCertsFromPEM(t.anchors[name].certificates)
	}
	t.roots = roots
}
