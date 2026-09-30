package main

// These tests count the reads persist sends when it reads the bond
// Secrets from the watch's store, and cover a write from a copy in the
// store that is older than the API server's, and a Secret the store
// has not delivered yet. The last test runs the Secret watch through
// client-go's reflector.

import (
	"bytes"
	"testing"
	"testing/synctest"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// secretStore is the watch's store, holding the Secrets given.
func secretStore(t *testing.T, secrets map[string]*bonds.Secret) informer.View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, secret := range secrets {
		if err := store.Add(asObject(t, *secret)); err != nil {
			t.Fatal(err)
		}
	}
	return informer.View{Store: store, Synced: func() bool { return true }}
}

// storedBondAt is the test device's bond as the API server holds it,
// at one resourceVersion.
func storedBondAt(t *testing.T, files bonds.Files, version string) map[string]*bonds.Secret {
	t.Helper()
	secrets := storedBond(t, testDevice, files)
	for _, secret := range secrets {
		secret.Metadata.Namespace = "liken-system"
		secret.Metadata.ResourceVersion = version
	}
	return secrets
}

// A settled bond costs one read of its Secret from the API server, and
// none from the store.
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
				t.Fatal("a settled bond reported unstored")
			}
			if len(fixture.requests) != c.want {
				t.Errorf("persist sent %v, want %d requests", fixture.requests, c.want)
			}
		})
	}
}

// The store's copy can be older than this operator's own last write.
// The write from it is refused, and persist reads the Secret again and
// writes once more.
func TestABondWriteFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, bonds.Files{Info: oneBond.Info}, "8")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, storedBondAt(t, bonds.Files{Info: oneBond.Info}, "7"))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the bond reported unstored")
	}
	if fixture.updated == nil || !bytes.Equal(fixture.updated.Data["a0-ab-51-33-b7-12.cache"], oneBond.Cache) {
		t.Fatalf("the cache entry did not land: %v", fixture.requests)
	}
	if fixture.updated.Metadata.ResourceVersion != "8" {
		t.Errorf("the write that landed stated version %q, want 8", fixture.updated.Metadata.ResourceVersion)
	}
}

// A conflict that another writer caused can leave the API server's
// copy already holding the bond on disk. persist compares that copy
// and writes nothing more.
func TestABondConflictWithACopyThatAlreadyMatchesWritesNothing(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, oneBond, "8")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, storedBondAt(t, bonds.Files{Info: oneBond.Info}, "7"))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the bond reported unstored")
	}
	want := []string{"PUT " + testSecretPath, "GET " + testSecretPath}
	if len(fixture.requests) != len(want) || fixture.requests[0] != want[0] || fixture.requests[1] != want[1] {
		t.Errorf("requests = %v, want %v", fixture.requests, want)
	}
	if fixture.updated != nil {
		t.Error("persist wrote a Secret that already held the bond")
	}
}

// A Secret that the store has not delivered, such as one this operator
// created on the pass before, is read from the API server and compared,
// not created twice.
func TestABondTheStoreHasNotDeliveredIsComparedNotCreated(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBondAt(t, oneBond, "7")}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.secrets = secretStore(t, nil)

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the bond reported unstored")
	}
	if len(fixture.requests) != 1 || fixture.requests[0] != "GET "+testSecretPath {
		t.Errorf("requests = %v, want one read of the Secret", fixture.requests)
	}
}

// Through the reflector: the Secret watch selects one radio's Secrets
// in the pod's namespace, and its store answers each Secret with the
// bytes of its bond.
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
			t.Fatal("the Secret watch did not finish its first read")
		}
		held, ok := informer.Cached[bonds.Secret](view, "liken-system/bluetooth-bond-a0-ab-51-33-b7-12")
		if !ok || !held.Tree()[testAddress(t, testDevice)].Equal(oneBond) {
			t.Fatalf("the store answered %+v, %t; want the bond", held, ok)
		}
		if _, selectors := server.held(); len(selectors) == 0 || selectors[0] != adapterSelector("14-b4-57-91-2f-c8") {
			t.Errorf("the watch selected %v, want %s", selectors, adapterSelector("14-b4-57-91-2f-c8"))
		}
	})
}
