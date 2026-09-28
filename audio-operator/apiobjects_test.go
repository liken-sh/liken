package main

// These tests run the two certificate object watches through the real
// reflector: the Secret the capture containers mount, and the
// ConfigMap that holds the cluster's client authority.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// namedObject is a named object of a kind at a version, with nothing
// else the watch reads.
func namedObject(kind, name, version string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "liken-system", "resourceVersion": version},
	}
}

// objectEvent is one watch event for a named object.
func objectEvent(t *testing.T, event, kind, name, version string) string {
	t.Helper()
	return encode(t, map[string]any{"type": event, "object": namedObject(kind, name, version)})
}

// The API follows the capture Secret with a watch, and a delete of the
// Secret is minted again when the event arrives. The hourly lifetime
// check never runs in this test, so only the event can bring the
// Secret back. Both watches select their one object by name, so RBAC
// restricts them to it.
func TestADeletedCaptureSecretIsMintedAgainWhenTheWatchReportsIt(t *testing.T) {
	store := newObjectStore(t)
	secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret",
		[]string{encode(t, []any{namedObject("Secret", captureTLSSecret, "1")})},
		[]string{pause, objectEvent(t, "DELETED", "Secret", captureTLSSecret, "2"), holdOpen})
	configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap", []string{`[]`})
	// checks receives each read of the capture Secret, which is the
	// first step of every leaf check.
	checks := make(chan struct{}, 16)
	objects := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == secretsPath("liken-system")+"/"+captureTLSSecret {
			checks <- struct{}{}
		}
		store.serve(w, r)
	})
	api := httptest.NewServer(serveCollections(t, objects, secrets, configMaps))
	t.Cleanup(api.Close)
	server := newAPIServer(NewClient(api.URL, api.Client(), ""), "liken-system")
	if _, err := server.certs.ensure(); err != nil {
		t.Fatal(err)
	}
	first := store.secretData(t, captureTLSSecret)[tlsCertFile]
	for len(checks) > 0 {
		<-checks
	}

	server.followCertificateObjects(watchContext(t), testWatcher(t, api.Config.Handler), func(error) {})
	// The Secret's add runs a check before the delete, so it cannot
	// mint the leaf again.
	next(t, checks, "leaf check for the first read")

	store.mu.Lock()
	delete(store.secrets, captureTLSSecret)
	store.mu.Unlock()
	secrets.release()

	deadline := time.Now().Add(5 * time.Second)
	for !store.holdsSecret(captureTLSSecret) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	again := store.secretData(t, captureTLSSecret)[tlsCertFile]
	if string(again) == string(first) {
		t.Error("the Secret carries the deleted leaf")
	}
	for _, c := range []struct {
		server *watchServer
		name   string
	}{{secrets, captureTLSSecret}, {configMaps, clientCAConfigMap}} {
		for _, query := range c.server.requests() {
			if !strings.Contains(query, "fieldSelector=metadata.name%3D"+c.name) {
				t.Errorf("the request %s?%s does not select %s by name", c.server.collection, query, c.name)
			}
		}
	}
}

// A capture Secret that does not exist when the watch starts arrives
// as no event, so the end of the first read runs the check that mints
// it.
func TestAnAbsentCaptureSecretIsMintedWhenTheFirstReadIsDone(t *testing.T) {
	store := newObjectStore(t)
	secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret", []string{`[]`})
	configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap", []string{`[]`})
	api := httptest.NewServer(serveCollections(t, http.HandlerFunc(store.serve), secrets, configMaps))
	t.Cleanup(api.Close)
	server := newAPIServer(NewClient(api.URL, api.Client(), ""), "liken-system")
	if _, err := server.certs.ensure(); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	delete(store.secrets, captureTLSSecret)
	store.mu.Unlock()

	server.followCertificateObjects(watchContext(t), testWatcher(t, api.Config.Handler), func(error) {})

	deadline := time.Now().Add(5 * time.Second)
	for !store.holdsSecret(captureTLSSecret) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !store.holdsSecret(captureTLSSecret) {
		t.Error("the capture Secret was not minted after the first read")
	}
}

// authorityConfigMap is the ConfigMap that holds the cluster's client
// authority, at a version.
func authorityConfigMap(caPEM []byte, version string) map[string]any {
	held := namedObject("ConfigMap", clientCAConfigMap, version)
	held["metadata"].(map[string]any)["namespace"] = clientCANamespace
	held["data"] = map[string]any{clientCAKey: string(caPEM)}
	return held
}

// Through the reflector: a change to the ConfigMap that holds the
// cluster's client authority loads the authority again, so a rotation
// takes effect with no restart. The copy the watch delivers is the one
// the pool takes, and the API sends no read of its own.
func TestARotatedClientAuthorityIsLoadedWhenTheWatchReportsIt(t *testing.T) {
	first, second := newClientAuthority(t), newClientAuthority(t)
	secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret", []string{`[]`})
	configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap",
		[]string{encode(t, []any{authorityConfigMap(first.certPEM, "1")})},
		[]string{pause, encode(t, map[string]any{"type": "MODIFIED", "object": authorityConfigMap(second.certPEM, "2")}), holdOpen})
	reads := make(chan string, 16)
	others := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, configMapsPath(clientCANamespace)) {
			reads <- r.URL.Path
		}
		http.NotFound(w, r)
	})
	api := httptest.NewServer(serveCollections(t, others, secrets, configMaps))
	t.Cleanup(api.Close)
	server := newAPIServer(NewClient(api.URL, api.Client(), ""), "liken-system")

	server.followCertificateObjects(watchContext(t), testWatcher(t, api.Config.Handler), func(error) {})
	configMaps.awaitWatches(t, 1)
	awaitVerifies(t, server.anchors, first)

	configMaps.release()
	awaitVerifies(t, server.anchors, second)
	if len(reads) != 0 {
		t.Errorf("the API read the ConfigMap %d times", len(reads))
	}
}

// An authority ConfigMap that does not exist leaves the pool as it was,
// and says so.
func TestAnAbsentClientAuthorityKeepsThePool(t *testing.T) {
	held := newClientAuthority(t)
	anchors := &clientAnchors{}
	if err := anchors.adopt(&configMap{Data: map[string]string{clientCAKey: string(held.certPEM)}}); err != nil {
		t.Fatal(err)
	}
	if err := anchors.adopt(nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("adopt(nil) = %v, want an error that says the ConfigMap does not exist", err)
	}
	if !verifiesAgainst(t, anchors, held) {
		t.Error("the pool lost the authority it held")
	}
}

// awaitVerifies waits until a leaf the authority signed verifies
// against the pool.
func awaitVerifies(t *testing.T, anchors *clientAnchors, authority *clientAuthority) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !verifiesAgainst(t, anchors, authority) {
		if time.Now().After(deadline) {
			t.Fatal("the authority's leaf does not verify against the pool")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
