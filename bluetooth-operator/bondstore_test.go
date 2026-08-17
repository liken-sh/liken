package main

// These tests cover the bond store's outcomes against a test API
// server and a BlueZ storage tree on disk: create a bond's Secret at
// the first pairing, update it when the bond changes, write nothing
// when the two already agree, write nothing for a bond that has no
// Pairing to own its Secret, and leave a bond alone while its teardown
// runs.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// testAdapter is the radio these tests pair against. It is the adapter
// the README's example Secret belongs to.
const testAdapter = "14:B4:57:91:2F:C8"

// The device these tests pair, and the paths its Secret lives at.
const (
	testDevice      = "A0:AB:51:33:B7:12"
	testSecretPath  = "/api/v1/namespaces/liken-system/secrets/bluetooth-bond-a0-ab-51-33-b7-12"
	testSecretsPath = "/api/v1/namespaces/liken-system/secrets"
	testLegacyPath  = "/api/v1/namespaces/liken-system/secrets/bluetooth-bonds-14-b4-57-91-2f-c8"
)

func testAdapterAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testAdapter)
}

func testAddress(t *testing.T, literal string) bonds.Address {
	t.Helper()
	address, err := bonds.ParseAddress(literal)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

// bondSecretFixture is a small API server that holds the bond Secrets
// by name. It remembers the requests it received, and writeStatus
// makes every write fail.
type bondSecretFixture struct {
	existing    map[string]*bonds.Secret
	created     *bonds.Secret
	updated     *bonds.Secret
	writeStatus int
	requests    []string
}

func (f *bondSecretFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet && f.writeStatus != 0 {
			w.WriteHeader(f.writeStatus)
			return
		}
		switch r.Method {
		case http.MethodGet:
			secret, found := f.existing[r.URL.Path]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(secret)
		case http.MethodPost:
			f.created = &bonds.Secret{}
			_ = json.NewDecoder(r.Body).Decode(f.created)
			_ = json.NewEncoder(w).Encode(f.created)
		case http.MethodPut:
			f.updated = &bonds.Secret{}
			_ = json.NewDecoder(r.Body).Decode(f.updated)
			_ = json.NewEncoder(w).Encode(f.updated)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

// storedBond is one bond as the API server already holds it, with the
// resourceVersion a read carries.
func storedBond(t *testing.T, device string, files bonds.Files) map[string]*bonds.Secret {
	t.Helper()
	address := testAddress(t, device)
	secret := bonds.NewBondSecret("liken-system", testAdapterAddress(t), address, files, bondOwner(testPairingOwner()))
	secret.Metadata.ResourceVersion = "7"
	return map[string]*bonds.Secret{bonds.BondSecretPath("liken-system", address): secret}
}

// testTree builds one adapter's bonds out of device addresses and
// their files.
func testTree(t *testing.T, devices map[string]bonds.Files) bonds.Tree {
	t.Helper()
	tree := bonds.Tree{}
	for address, files := range devices {
		tree[testAddress(t, address)] = files
	}
	return tree
}

// bondTree writes a BlueZ storage tree for the test adapter under a
// temporary root, and answers with that root. A nil device map still
// creates the adapter's own directory, which is the state of an
// adapter whose last device was unpaired.
func bondTree(t *testing.T, devices map[string]bonds.Files) string {
	t.Helper()
	root := t.TempDir()
	if err := bonds.WriteTree(root, testAdapterAddress(t), testTree(t, devices)); err != nil {
		t.Fatal(err)
	}
	return root
}

// testBondStore points a store at the fixture's API server and at a
// storage tree the test owns. The legacy report is marked as already
// made, because it is one line for a person and not a behavior these
// tests drive.
func testBondStore(t *testing.T, fixture *bondSecretFixture, root string) *bondStore {
	t.Helper()
	return &bondStore{
		client:         testClient(t, fixture.handler(t)),
		namespace:      "liken-system",
		root:           root,
		reportedLegacy: true,
	}
}

// testPairingOwner is the Pairing that owns the test device's Secret.
func testPairingOwner() OwnerReference {
	return OwnerReference{
		APIVersion: pairingAPI,
		Kind:       pairingKind,
		Name:       "a0-ab-51-33-b7-12",
		UID:        "9c1a2f10-0000-4000-8000-000000000002",
	}
}

// ownedBy names the Pairing for each of these devices, which is what
// the inventory pass hands the store.
func ownedBy(t *testing.T, devices ...string) map[bonds.Address]OwnerReference {
	t.Helper()
	owners := map[bonds.Address]OwnerReference{}
	for _, device := range devices {
		owner := testPairingOwner()
		owner.Name = strings.ReplaceAll(strings.ToLower(device), ":", "-")
		owners[testAddress(t, device)] = owner
	}
	return owners
}

// adapterIs answers with the test adapter, the way bluetoothd's
// Adapter1 interface does once it has published its object tree.
func adapterIs(t *testing.T) adapterAddressReader {
	address := testAdapterAddress(t)
	return func() (bonds.Address, error) { return address, nil }
}

// The two files BlueZ writes for a paired controller: the info file
// under the device's own directory, and the cache entry that holds its
// SDP records. Nothing here parses either, and these tests carry short
// ones for the same reason the operator does: the bytes travel and
// their meaning does not.
var oneBond = bonds.Files{
	Info:  []byte("[General]\nName=DualSense Wireless Controller\nAddressType=public\n"),
	Cache: []byte("[ServiceRecords]\n0x00010000=35760900000A00010000\n"),
}

// playerTwo is a second controller, paired and with no cache entry
// yet. bluetoothd writes the cache entry when it resolves the device's
// name, so a device holds a bond before it holds SDP records.
var playerTwo = bonds.Files{Info: []byte("[General]\nName=Player Two\nAddressType=random\n")}

func TestPersistCreatesTheSecretAtTheFirstPairing(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the first pairing reported the bond unstored")
	}
	secret := fixture.created
	if secret == nil {
		t.Fatal("no Secret was created")
	}
	if secret.Metadata.Name != "bluetooth-bond-a0-ab-51-33-b7-12" {
		t.Errorf("name = %q", secret.Metadata.Name)
	}
	if secret.Type != bonds.SecretType {
		t.Errorf("type = %q", secret.Type)
	}
	// The label names the radio, which is what the init container lists
	// by, and the owner is the Pairing, which is what collects the keys
	// when somebody unpairs the controller.
	if secret.Metadata.Labels[bonds.AdapterLabel] != "14-b4-57-91-2f-c8" {
		t.Errorf("labels = %+v", secret.Metadata.Labels)
	}
	if len(secret.Metadata.OwnerReferences) != 1 ||
		secret.Metadata.OwnerReferences[0].Kind != pairingKind ||
		secret.Metadata.OwnerReferences[0].Name != "a0-ab-51-33-b7-12" {
		t.Errorf("ownerReferences = %+v", secret.Metadata.OwnerReferences)
	}
	// Both of the device's files travel, each under its own key. The
	// cache entry holds the SDP records, and a BR/EDR HID device does
	// not reconnect without them.
	if got := string(secret.Data["a0-ab-51-33-b7-12.info"]); got != string(oneBond.Info) {
		t.Errorf("the info file arrived as %q", got)
	}
	if got := string(secret.Data["a0-ab-51-33-b7-12.cache"]); got != string(oneBond.Cache) {
		t.Errorf("the cache entry arrived as %q", got)
	}
	// A create names the collection and a read names the object.
	want := []string{"GET " + testSecretPath, "POST " + testSecretsPath}
	if len(fixture.requests) != 2 || fixture.requests[0] != want[0] || fixture.requests[1] != want[1] {
		t.Errorf("requests = %v, want %v", fixture.requests, want)
	}
}

func TestPersistCreatesNothingForAnAdapterThatPairedNothing(t *testing.T) {
	// A machine that has paired nothing has no bonds to store, and an
	// empty Secret says nothing that its absence does not.
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, nil))

	if !store.persist(adapterIs(t), nil, nil) {
		t.Fatal("an adapter with no bonds reported a bond unstored")
	}
	if fixture.created != nil {
		t.Fatalf("an empty tree created a Secret: %+v", fixture.created)
	}
}

// A device pairs first and gets its cache entry when bluetoothd
// resolves its name, so the two files can land on different passes.
// The second file has to reach the Secret on its own: a BR/EDR HID
// device restored from an info file alone connects and drops again,
// because the input profile finds no HID SDP record.
func TestPersistCarriesACacheEntryThatLandsAfterThePairing(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBond(t, testDevice, bonds.Files{Info: oneBond.Info})}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("a new cache entry reported the bond unstored")
	}
	if fixture.updated == nil {
		t.Fatal("the new cache entry was not written")
	}
	if got := string(fixture.updated.Data["a0-ab-51-33-b7-12.cache"]); got != string(oneBond.Cache) {
		t.Errorf("the cache entry arrived as %q", got)
	}
	// The write carries the resourceVersion from the read, so a second
	// writer gets a conflict instead of losing the first writer's bond.
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q", fixture.updated.Metadata.ResourceVersion)
	}
}

// The adapter's cache directory holds one entry for every device the
// radio has resolved a name for, which in a house is the neighbours'
// phones. A device with no directory of its own is not paired to this
// adapter, and nothing about it may reach the API.
func TestPersistCarriesNoCacheEntryForADeviceThatIsNotPaired(t *testing.T) {
	root := bondTree(t, map[string]bonds.Files{testDevice: oneBond})
	neighbour := filepath.Join(root, testAdapter, "cache", "E3:28:E9:23:21:6F")
	if err := os.WriteFile(neighbour, []byte("[General]\nName=Somebody's Phone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, root)

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the first pairing reported the bond unstored")
	}
	if fixture.created == nil {
		t.Fatal("no Secret was created")
	}
	for key, value := range fixture.created.Data {
		if strings.HasPrefix(key, "e3-28-e9-23-21-6f") {
			t.Errorf("the Secret carries %s, a device this adapter never paired with: %q", key, value)
		}
	}
}

func TestPersistWritesNothingWhenTheBondMatchesTheSecret(t *testing.T) {
	// bluetoothd rewrites its tree for reasons that change no key, and
	// the loop passes once a minute with no event at all. A write on
	// every pass would be a write to the API server on every pass.
	fixture := &bondSecretFixture{existing: storedBond(t, testDevice, oneBond)}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("a steady adapter reported a bond unstored")
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Fatalf("a steady adapter wrote to the API: %v", fixture.requests)
	}
}

// A bond whose Pairing has not been created yet gets no Secret. An
// owner reference cannot be added to a Secret that has none, so a
// Secret written now would be one nothing ever collects.
func TestPersistWaitsForTheBondToHaveAPairing(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if store.persist(adapterIs(t), nil, nil) {
		t.Fatal("a bond with no Pairing reported the bond stored")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a bond with no Pairing reached the API: %v", fixture.requests)
	}
}

// A bond under teardown is on its way out. Its Secret is not rewritten,
// and the Pairing that owns it is what collects it.
func TestPersistLeavesABondUnderTeardownAlone(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	unpairing := map[bonds.Address]bool{testAddress(t, testDevice): true}

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), unpairing) {
		t.Fatal("a teardown reported a bond unstored")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a bond under teardown reached the API: %v", fixture.requests)
	}
}

// An unpaired device's Secret is not emptied and not deleted here.
// Deleting a Pairing is what removes a bond, and the Secret goes with
// the Pairing through its owner reference.
func TestPersistWritesNothingForABondThatLeftTheTree(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedBond(t, testDevice, oneBond)}
	store := testBondStore(t, fixture, bondTree(t, nil))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("an empty tree reported a bond unstored")
	}
	if fixture.updated != nil || fixture.created != nil {
		t.Fatalf("an empty tree wrote to the API: %v", fixture.requests)
	}
}

func TestPersistStoresEveryBondOnDisk(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		testDevice:          oneBond,
		"B4:8C:9D:11:22:33": playerTwo,
	}))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice, "B4:8C:9D:11:22:33"), nil) {
		t.Fatal("two bonds reported one unstored")
	}
	creates := 0
	for _, request := range fixture.requests {
		if strings.HasPrefix(request, "POST ") {
			creates++
		}
	}
	if creates != 2 {
		t.Errorf("requests = %v, want a create for each bond", fixture.requests)
	}
}

func TestPersistReadsTheAdapterAddressOnce(t *testing.T) {
	// The pod's bondfetch restored one radio's keys, and the operator
	// persists that radio's tree for the life of the pod. A second read
	// could answer with a different adapter, whose Secrets this pod
	// never restored and whose tree is therefore not on this disk.
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	reads := 0
	address := testAdapterAddress(t)
	read := func() (bonds.Address, error) {
		reads++
		return address, nil
	}
	owners := ownedBy(t, testDevice)

	if !store.persist(read, owners, nil) || !store.persist(read, owners, nil) {
		t.Fatal("a pass reported a bond unstored")
	}
	if reads != 1 {
		t.Fatalf("the address was read %d times, want 1", reads)
	}
}

func TestPersistWaitsForBlueZToPublishAnAdapter(t *testing.T) {
	// bluetoothd publishes its object tree a moment after it claims its
	// bus name. Until it does, the operator does not know which radio
	// the bonds on disk belong to.
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	read := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }

	if store.persist(read, ownedBy(t, testDevice), nil) {
		t.Fatal("a pass with no adapter reported the bonds stored")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a pass with no adapter reached the API: %v", fixture.requests)
	}
}

func TestPersistReportsAFailedWrite(t *testing.T) {
	// A failed write is logged and reported, and nothing else. The next
	// trigger or the next backstop pass writes again, and the worst
	// case is that somebody pairs a controller again.
	fixture := &bondSecretFixture{writeStatus: http.StatusInternalServerError}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("a failed write reported the bond stored")
	}
}

// The migration leaves the older per-adapter Secret in place and names
// it once. bondfetch still reads it, so the bonds restore, and a
// person deletes the object after a drill has shown the controllers
// reconnecting from the per-bond Secrets.
func TestPersistReportsTheOlderPerAdapterSecretAndKeepsIt(t *testing.T) {
	fixture := &bondSecretFixture{existing: map[string]*bonds.Secret{
		testLegacyPath: {Metadata: bonds.SecretMeta{Name: "bluetooth-bonds-14-b4-57-91-2f-c8"}},
	}}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.reportedLegacy = false

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the pass reported the bond unstored")
	}
	if !store.reportedLegacy {
		t.Error("the older Secret was not reported")
	}
	for _, request := range fixture.requests {
		if strings.HasSuffix(request, testLegacyPath) && !strings.HasPrefix(request, "GET ") {
			t.Errorf("the migration wrote to the older Secret: %v", fixture.requests)
		}
	}

	// The second pass reads it no more, because the report is for a
	// person and not a state to watch.
	fixture.requests = nil
	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the second pass reported the bond unstored")
	}
	for _, request := range fixture.requests {
		if strings.HasSuffix(request, testLegacyPath) {
			t.Errorf("the older Secret was read again: %v", fixture.requests)
		}
	}
}
