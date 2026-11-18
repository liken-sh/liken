package main

import (
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	testIdentityPath = "/api/v1/namespaces/liken-system/secrets/bluetooth-identity-14-b4-57-91-2f-c8"
	testIdentity     = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"
	newIdentity      = "[General]\nIdentityResolvingKey=FEDCBA9876543210FEDCBA9876543210\n"
)

// testAdapterObject has the UID for the Secret's owner reference.
func testAdapterObject() *Adapter {
	return &Adapter{Metadata: ObjectMeta{Name: testAdapterName, UID: "uid-" + testAdapterName}}
}

// identityTree returns the root of a BlueZ storage tree whose adapter
// has an identity file with these contents.
func identityTree(t *testing.T, identity string) string {
	t.Helper()
	root := bondTree(t, nil)
	if err := bonds.WriteIdentity(root, testAdapterAddress(t), []byte(identity)); err != nil {
		t.Fatalf("writing the identity file: %v", err)
	}
	return root
}

// identityStore returns a store that already has the adapter address,
// which persist reads earlier in the same pass.
func identityStore(t *testing.T, fixture *bondSecretFixture, root string) *bondStore {
	t.Helper()
	store := testBondStore(t, fixture, root)
	store.useAdapter(testAdapterAddress(t))
	return store
}

func TestPersistIdentityCreatesTheSecretWhenTheFileAppears(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}

	if fixture.created == nil {
		t.Fatalf("no Secret was created: %v", fixture.requests)
	}
	if fixture.created.Metadata.Name != "bluetooth-identity-14-b4-57-91-2f-c8" {
		t.Errorf("the Secret name is %q, want bluetooth-identity-14-b4-57-91-2f-c8", fixture.created.Metadata.Name)
	}
	if string(fixture.created.Identity()) != testIdentity {
		t.Errorf("the Secret holds the identity %q, want %q", fixture.created.Identity(), testIdentity)
	}
	owners := fixture.created.Metadata.OwnerReferences
	if len(owners) != 1 || owners[0].Kind != adapterKind || owners[0].UID != "uid-"+testAdapterName {
		t.Errorf("ownerReferences are %+v, want one reference to the Adapter", owners)
	}
}

// storedIdentity returns the fixture's existing Secrets: an identity
// Secret with these contents.
func storedIdentity(t *testing.T, identity string) map[string]*bonds.Secret {
	t.Helper()
	secret := bonds.NewIdentitySecret("liken-system", testAdapterAddress(t), []byte(identity), bonds.Owner{})
	secret.Metadata.ResourceVersion = "7"
	return map[string]*bonds.Secret{testIdentityPath: secret}
}

func TestPersistIdentityUpdatesTheSecretWhenTheFileChanges(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedIdentity(t, testIdentity)}
	store := identityStore(t, fixture, identityTree(t, newIdentity))

	if !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}

	if fixture.updated == nil || string(fixture.updated.Identity()) != newIdentity {
		t.Fatalf("updated = %+v, requests = %v", fixture.updated, fixture.requests)
	}
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q, want the version the read returned", fixture.updated.Metadata.ResourceVersion)
	}
}

// Only the first pass reads the Secret. Later passes compare the file
// with the cached contents and send no request.
func TestPersistIdentityWritesNothingWhileTheFileMatches(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedIdentity(t, testIdentity)}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if !store.persistIdentity(testAdapterObject()) || !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}

	if want := []string{"GET " + testIdentityPath}; len(fixture.requests) != 1 || fixture.requests[0] != want[0] {
		t.Errorf("requests = %v, want %v", fixture.requests, want)
	}
}

// With no identity file in the tree, the store sends no request and
// keeps the Secret.
func TestPersistIdentityLeavesTheSecretWithNoFile(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedIdentity(t, testIdentity)}
	store := identityStore(t, fixture, bondTree(t, nil))

	if !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}

	if len(fixture.requests) != 0 {
		t.Errorf("requests = %v, want none", fixture.requests)
	}
}

// persistIdentity returns false so that a later pass retries.
func TestPersistIdentityWritesNothingWithoutAnAdapterObject(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if store.persistIdentity(nil) {
		t.Error("the store reported the key stored with no Adapter to own it")
	}
	if len(fixture.requests) != 0 {
		t.Errorf("requests = %v, want none", fixture.requests)
	}
}

func TestPersistIdentityReportsAFailedWrite(t *testing.T) {
	fixture := &bondSecretFixture{writeStatus: 500}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported success for a write that failed")
	}
	fixture.writeStatus = 0
	if !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}
	if fixture.created == nil {
		t.Errorf("the second pass did not create the Secret: %v", fixture.requests)
	}
}
