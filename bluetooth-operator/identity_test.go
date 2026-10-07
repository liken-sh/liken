package main

// These tests cover the store of the radio's identity file: the store
// pass writes the identity Secret when the file appears and when it
// changes, writes nothing while the two agree, and keeps the Secret
// when the file is gone.

import (
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	testIdentityPath = "/api/v1/namespaces/liken-system/secrets/bluetooth-identity-14-b4-57-91-2f-c8"
	testIdentity     = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"
	newIdentity      = "[General]\nIdentityResolvingKey=FEDCBA9876543210FEDCBA9876543210\n"
)

// testAdapterObject is the test radio's Adapter as the pass hands it
// on, with the UID the identity Secret's owner reference needs.
func testAdapterObject() *Adapter {
	return &Adapter{Metadata: ObjectMeta{Name: testAdapterName, UID: "uid-" + testAdapterName}}
}

// identityTree answers a storage tree whose adapter holds an identity
// file with these contents.
func identityTree(t *testing.T, identity string) string {
	t.Helper()
	root := bondTree(t, nil)
	if err := bonds.WriteIdentity(root, testAdapterAddress(t), []byte(identity)); err != nil {
		t.Fatal(err)
	}
	return root
}

// identityStore points a store at the fixture and at a tree, with the
// radio already learned, as it is after the bond pass of the same
// pass.
func identityStore(t *testing.T, fixture *bondSecretFixture, root string) *bondStore {
	t.Helper()
	store := testBondStore(t, fixture, root)
	store.learn(testAdapterAddress(t))
	return store
}

// The first pass after bluetoothd writes the file creates the Secret,
// owned by the Adapter.
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
		t.Errorf("name = %q", fixture.created.Metadata.Name)
	}
	if string(fixture.created.Identity()) != testIdentity {
		t.Errorf("identity = %q", fixture.created.Identity())
	}
	owners := fixture.created.Metadata.OwnerReferences
	if len(owners) != 1 || owners[0].Kind != adapterKind || owners[0].UID != "uid-"+testAdapterName {
		t.Errorf("ownerReferences = %+v", owners)
	}
}

// storedIdentity is the identity Secret as the API server already holds
// it.
func storedIdentity(t *testing.T, identity string) map[string]*bonds.Secret {
	t.Helper()
	secret := bonds.NewIdentitySecret("liken-system", testAdapterAddress(t), []byte(identity), bonds.Owner{})
	secret.Metadata.ResourceVersion = "7"
	return map[string]*bonds.Secret{testIdentityPath: secret}
}

// A file that differs from the Secret replaces the Secret's copy.
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

// A file that agrees with the Secret writes nothing, and the passes
// after the first send no request at all.
func TestPersistIdentityWritesNothingWhileTheTwoAgree(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedIdentity(t, testIdentity)}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if !store.persistIdentity(testAdapterObject()) || !store.persistIdentity(testAdapterObject()) {
		t.Fatal("the store reported a failure")
	}

	if want := []string{"GET " + testIdentityPath}; len(fixture.requests) != 1 || fixture.requests[0] != want[0] {
		t.Errorf("requests = %v, want %v", fixture.requests, want)
	}
}

// A radio with privacy off has no file. The store sends nothing, and a
// Secret from an earlier time with privacy on stays.
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

// A file with no Adapter to own its Secret waits for a pass that has
// one, and reports that the key is not stored yet.
func TestPersistIdentityWaitsForTheAdapter(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := identityStore(t, fixture, identityTree(t, testIdentity))

	if store.persistIdentity(nil) {
		t.Error("the store reported the key stored with no Adapter to own it")
	}
	if len(fixture.requests) != 0 {
		t.Errorf("requests = %v, want none", fixture.requests)
	}
}

// A write that fails reports the failure, and the next pass sends it
// again.
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
