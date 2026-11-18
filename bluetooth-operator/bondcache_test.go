package main

// These tests cover how persist reads the bond Secrets from the
// informer store of the Secret watch instead of from the API server.

import (
	"bytes"
	"testing"
	"testing/synctest"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// secretStore returns an informer view whose store holds secrets.
func secretStore(t *testing.T, secrets map[string]*bonds.Secret) informer.View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, secret := range secrets {
		if err := store.Add(asObject(t, *secret)); err != nil {
			t.Fatalf("adding the Secret to the store: %v", err)
		}
	}
	return informer.View{Store: store, Synced: func() bool { return true }}
}

// storedBondAt returns the test device's bond Secrets as the API server
// has them, at the given resourceVersion.
func storedBondAt(t *testing.T, files bonds.Files, version string) map[string]*bonds.Secret {
	t.Helper()
	secrets := storedBond(t, testDevice, files)
	for _, secret := range secrets {
		secret.Metadata.Namespace = "liken-system"
		secret.Metadata.ResourceVersion = version
	}
	return secrets
}

// When the bond is already stored, persist sends one GET when it has no
// store, and no request when the store has the Secret.
func TestPersistFromTheStoreSendsNoRead(t *testing.T) {
	for _, c := range []struct {
		name   string
		cached bool
		want   int
	}{
		{"the API server", false, 1},
		{"the store", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := &bondSecretFixture{existing: storedBondAt(t, oneBond, "7")}
			store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
			if c.cached {
				store.secrets = secretStore(t, fixture.existing)
			}

			if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
				t.Fatal("persist returned false for a bond that is already stored, want true")
			}
			if len(fixture.requests) != c.want {
				t.Errorf("persist sent %v, want %d requests", fixture.requests, c.want)
			}
		})
	}
}

// When the store's copy is older than the API server's, the update
// fails with a conflict. persist then reads the Secret from the API
// server and updates it again.
func TestABondWriteFromAnOlderCopyReadsAgainAndSucceeds(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, bonds.Files{Info: oneBond.Info}, "8")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, storedBondAt(t, bonds.Files{Info: oneBond.Info}, "7"))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("persist returned false, want true")
	}
	if fixture.updated == nil || !bytes.Equal(fixture.updated.Data["a0-ab-51-33-b7-12.cache"], oneBond.Cache) {
		t.Fatalf("the update has no cache entry for the bond, requests were %v", fixture.requests)
	}
	if fixture.updated.Metadata.ResourceVersion != "8" {
		t.Errorf("the update has resourceVersion %q, want 8", fixture.updated.Metadata.ResourceVersion)
	}
}

// When the update conflicts and the API server's copy already has the
// bond that is on disk, persist writes nothing more.
func TestABondConflictWithACopyThatAlreadyMatchesWritesNothing(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, oneBond, "8")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, storedBondAt(t, bonds.Files{Info: oneBond.Info}, "7"))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("persist returned false, want true")
	}
	want := []string{"PUT " + testSecretPath, "GET " + testSecretPath}
	if len(fixture.requests) != len(want) || fixture.requests[0] != want[0] || fixture.requests[1] != want[1] {
		t.Errorf("persist sent %v, want %v", fixture.requests, want)
	}
	if fixture.updated != nil {
		t.Error("persist updated a Secret that already has the bond, want no update")
	}
}

// When a Secret is not in the store yet, such as one the operator
// created on the previous pass, persist reads it from the API server and
// does not create it again.
func TestABondMissingFromTheStoreIsReadNotCreated(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, oneBond, "7")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, nil)

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("persist returned false, want true")
	}
	if len(fixture.requests) != 1 || fixture.requests[0] != "GET "+testSecretPath {
		t.Errorf("persist sent %v, want one read of the Secret", fixture.requests)
	}
}

// The Secret watch selects the bond Secrets of one adapter in the pod's
// namespace, and its store returns each Secret's bond files.
func TestTheSecretWatchHoldsTheRadiosBonds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		secrets := storedBondAt(t, oneBond, "7")
		var encoded string
		for _, secret := range secrets {
			secret.APIVersion, secret.Kind = "v1", "Secret"
			encoded = encode(t, secret)
		}
		server := newWatchServer(testSecretsPath, "SecretList", []string{"[" + encoded + "]"})

		view := watchBondSecrets(t.Context(), testWatcher(t, server.handler(t)), "liken-system", testAdapterAddress(t))
		synctest.Wait()

		if !view.Ready() {
			t.Fatal("the Secret watch is not ready after its first list, want ready")
		}
		cached, ok := informer.Cached[bonds.Secret](view, "liken-system/bluetooth-bond-a0-ab-51-33-b7-12")
		if !ok || !cached.Tree()[testAddress(t, testDevice)].Equal(oneBond) {
			t.Fatalf("the store returned %+v, %t, want the bond of the test device", cached, ok)
		}
		if _, selectors := server.openWatches(); len(selectors) == 0 || selectors[0] != adapterSelector("14-b4-57-91-2f-c8") {
			t.Errorf("the watch label selectors are %v, want %s", selectors, adapterSelector("14-b4-57-91-2f-c8"))
		}
	})
}
