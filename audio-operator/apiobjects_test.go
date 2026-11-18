package main

// These tests run followCertificateObjects through client-go's
// reflector: the watch of the Secret that the capture containers mount,
// and the watch of the ConfigMap that holds the cluster's client CA.

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// namedObject returns an object with only the fields that the watch
// reads.
func namedObject(kind, name, version string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": "liken-system", "resourceVersion": version},
	}
}

func objectEvent(t *testing.T, event, kind, name, version string) string {
	t.Helper()
	return encode(t, map[string]any{"type": event, "object": namedObject(kind, name, version)})
}

// When the watch reports that the capture Secret was deleted, the API
// creates it again. The hourly certificateCheck does not run in this
// test, so only the watch event can create the new Secret. Each watch
// selects its object with a fieldSelector on the name, so RBAC can
// limit the API to that object.
func TestADeletedCaptureSecretIsMintedAgainWhenTheWatchReportsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newObjectStore(t)
		secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret",
			[]string{encode(t, []any{namedObject("Secret", captureTLSSecret, "1")})},
			[]string{pause, objectEvent(t, "DELETED", "Secret", captureTLSSecret, "2"), holdOpen})
		configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap", []string{`[]`})
		// checks counts the GETs of the capture Secret. Each run of
		// keepCaptureLeaf starts with one.
		var checks atomic.Int64
		objects := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == secretsPath("liken-system")+"/"+captureTLSSecret {
				checks.Add(1)
			}
			store.serve(w, r)
		})
		api := apiservertest.Start(t, serveCollections(t, objects, secrets, configMaps))
		server := newAPIServer(apiclient.New(apiservertest.Host, api.Client(), ""), "liken-system")
		if _, err := server.certs.ensure(); err != nil {
			t.Fatalf("preparing the certificates: %v", err)
		}
		first := store.secretData(t, captureTLSSecret)[tlsCertFile]
		checks.Store(0)

		server.followCertificateObjects(t.Context(), dynamicClient(t, api), func(error) {})
		synctest.Wait()
		// The add event for the Secret runs keepCaptureLeaf once. The
		// Secret is still present then, so it creates nothing.
		if got := checks.Load(); got != 1 {
			t.Errorf("the first read ran %d leaf checks, want 1", got)
		}

		store.mu.Lock()
		delete(store.secrets, captureTLSSecret)
		store.mu.Unlock()
		secrets.release()
		synctest.Wait()

		again := store.secretData(t, captureTLSSecret)[tlsCertFile]
		if string(again) == string(first) {
			t.Error("the Secret carries the deleted leaf, want a leaf minted again")
		}
		for _, c := range []struct {
			server *watchServer
			name   string
		}{{secrets, captureTLSSecret}, {configMaps, clientCAConfigMap}} {
			if len(c.server.requests()) == 0 {
				t.Errorf("the watch sent no request for %s", c.name)
			}
			for _, query := range c.server.requests() {
				if !strings.Contains(query, "fieldSelector=metadata.name%3D"+c.name) {
					t.Errorf("the request %s?%s does not select %s by name", c.server.collection, query, c.name)
				}
			}
		}
	})
}

// A capture Secret that does not exist when the watch starts produces
// no event, so keepCaptureLeaf creates it when the first list returns.
func TestAnAbsentCaptureSecretIsMintedWhenTheFirstReadIsDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newObjectStore(t)
		secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret", []string{`[]`})
		configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap", []string{`[]`})
		api := apiservertest.Start(t, serveCollections(t, http.HandlerFunc(store.serve), secrets, configMaps))
		server := newAPIServer(apiclient.New(apiservertest.Host, api.Client(), ""), "liken-system")
		if _, err := server.certs.ensure(); err != nil {
			t.Fatalf("preparing the certificates: %v", err)
		}
		store.mu.Lock()
		delete(store.secrets, captureTLSSecret)
		store.mu.Unlock()

		server.followCertificateObjects(t.Context(), dynamicClient(t, api), func(error) {})
		synctest.Wait()

		if !store.holdsSecret(captureTLSSecret) {
			t.Error("the capture Secret is absent after the first read, want it minted")
		}
	})
}

func authorityConfigMap(caPEM []byte, version string) map[string]any {
	object := namedObject("ConfigMap", clientCAConfigMap, version)
	object["metadata"].(map[string]any)["namespace"] = clientCANamespace
	object["data"] = map[string]any{clientCAKey: string(caPEM)}
	return object
}

// A change to the client CA ConfigMap reloads the CA pool, so a
// rotation takes effect without a restart. The pool is built from the
// object that the watch delivers, and the API makes no GET of its own.
func TestARotatedClientAuthorityIsLoadedWhenTheWatchReportsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := newClientAuthority(t), newClientAuthority(t)
		secrets := newWatchServer(secretsPath("liken-system"), "v1", "Secret", []string{`[]`})
		configMaps := newWatchServer(configMapsPath(clientCANamespace), "v1", "ConfigMap",
			[]string{encode(t, []any{authorityConfigMap(first.certPEM, "1")})},
			[]string{pause, encode(t, map[string]any{"type": "MODIFIED", "object": authorityConfigMap(second.certPEM, "2")}), holdOpen})
		var reads atomic.Int64
		others := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, configMapsPath(clientCANamespace)) {
				reads.Add(1)
			}
			http.NotFound(w, r)
		})
		api := apiservertest.Start(t, serveCollections(t, others, secrets, configMaps))
		server := newAPIServer(apiclient.New(apiservertest.Host, api.Client(), ""), "liken-system")

		server.followCertificateObjects(t.Context(), dynamicClient(t, api), func(error) {})
		synctest.Wait()
		if !verifiesAgainst(t, server.anchors, first) {
			t.Error("the first authority's leaf does not verify against the pool")
		}

		configMaps.release()
		synctest.Wait()
		if !verifiesAgainst(t, server.anchors, second) {
			t.Error("the rotated authority's leaf does not verify against the pool")
		}
		if got := reads.Load(); got != 0 {
			t.Errorf("the API read the ConfigMap %d times, want 0", got)
		}
	})
}

// When the client CA ConfigMap does not exist, adopt returns an error
// and keeps the CA pool it had.
func TestAnAbsentClientAuthorityKeepsThePool(t *testing.T) {
	authority := newClientAuthority(t)
	anchors := &clientAnchors{}
	if err := anchors.adopt(&configMap{Data: map[string]string{clientCAKey: string(authority.certPEM)}}); err != nil {
		t.Fatalf("adopting the first authority: %v", err)
	}
	if err := anchors.adopt(nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("adopt(nil) = %v, want an error that says the ConfigMap does not exist", err)
	}
	if !verifiesAgainst(t, anchors, authority) {
		t.Error("the pool lost the authority it had, want it kept")
	}
}
