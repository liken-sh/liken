package main

// These tests run the bond store against a fake API server and a BlueZ
// storage tree on disk, and check which requests the store sends.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const testAdapter = "14:B4:57:91:2F:C8"

const (
	testDevice      = "A0:AB:51:33:B7:12"
	testSecretPath  = "/api/v1/namespaces/liken-system/secrets/bluetooth-bond-a0-ab-51-33-b7-12"
	testSecretsPath = "/api/v1/namespaces/liken-system/secrets"
	// testOlderSecretPath is the per-adapter Secret that bondfetch also
	// reads.
	testOlderSecretPath = "/api/v1/namespaces/liken-system/secrets/bluetooth-bonds-14-b4-57-91-2f-c8"
)

func testAdapterAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testAdapter)
}

func testAddress(t *testing.T, literal string) bonds.Address {
	t.Helper()
	address, err := bonds.ParseAddress(literal)
	if err != nil {
		t.Fatalf("parsing the address %q: %v", literal, err)
	}
	return address
}

// bondSecretFixture is a fake API server for bond Secrets, keyed by
// path, that records each request. When writeStatus is not zero, every
// write fails with that status. It returns 409 Conflict for a create of
// a Secret that exists and for an update whose resourceVersion differs
// from the stored one.
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
			created := &bonds.Secret{}
			_ = json.NewDecoder(r.Body).Decode(created)
			if _, exists := f.existing[r.URL.Path+"/"+created.Metadata.Name]; exists {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.created = created
			_ = json.NewEncoder(w).Encode(f.created)
		case http.MethodPut:
			updated := &bonds.Secret{}
			_ = json.NewDecoder(r.Body).Decode(updated)
			if stored, found := f.existing[r.URL.Path]; found &&
				stored.Metadata.ResourceVersion != updated.Metadata.ResourceVersion {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.updated = updated
			_ = json.NewEncoder(w).Encode(f.updated)
		default:
			t.Errorf("the fixture received %s %s, which it does not serve", r.Method, r.URL.Path)
		}
	})
}

// storedBond returns one bond's Secret, at resourceVersion 7, for the
// fixture's existing map.
func storedBond(t *testing.T, device string, files bonds.Files) map[string]*bonds.Secret {
	t.Helper()
	address := testAddress(t, device)
	secret := bonds.NewBondSecret("liken-system", testAdapterAddress(t), address, files, nil, bondOwner(testPeripheralOwner()))
	secret.Metadata.ResourceVersion = "7"
	return map[string]*bonds.Secret{bonds.BondSecretPath("liken-system", address): secret}
}

func testTree(t *testing.T, devices map[string]bonds.Files) bonds.Tree {
	t.Helper()
	tree := bonds.Tree{}
	for address, files := range devices {
		tree[testAddress(t, address)] = files
	}
	return tree
}

// bondTree writes a BlueZ storage tree for the test adapter into a
// temporary directory. A nil map still creates the adapter's directory,
// as BlueZ leaves it after the last device is unpaired.
func bondTree(t *testing.T, devices map[string]bonds.Files) string {
	t.Helper()
	root := t.TempDir()
	if err := bonds.WriteTree(root, testAdapterAddress(t), testTree(t, devices)); err != nil {
		t.Fatalf("writing the bond tree: %v", err)
	}
	return root
}

// testBondStore sets reportedOlderSecret so that the older Secret is
// not looked up, except in the test that checks it.
func testBondStore(t *testing.T, fixture *bondSecretFixture, root string) *bondStore {
	t.Helper()
	return &bondStore{
		client:              testClient(t, fixture.handler(t)),
		namespace:           "liken-system",
		root:                root,
		relays:              relaysFor(t),
		reportedOlderSecret: true,
	}
}

func testPeripheralOwner() OwnerReference {
	return OwnerReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       "a0-ab-51-33-b7-12",
		UID:        "9c1a2f10-0000-4000-8000-000000000002",
	}
}

// ownedBy returns the owners map for persist, with a Peripheral for
// each device.
func ownedBy(t *testing.T, devices ...string) map[bonds.Address]OwnerReference {
	t.Helper()
	owners := map[bonds.Address]OwnerReference{}
	for _, device := range devices {
		owner := testPeripheralOwner()
		owner.Name = strings.ReplaceAll(strings.ToLower(device), ":", "-")
		owners[testAddress(t, device)] = owner
	}
	return owners
}

func adapterIs(t *testing.T) adapterAddressReader {
	address := testAdapterAddress(t)
	return func() (bonds.Address, error) { return address, nil }
}

// oneBond has the info file and the cache entry, with its SDP records,
// that BlueZ writes for a paired controller. The operator copies the
// bytes without parsing them, so the contents are short.
var oneBond = bonds.Files{
	Info:  []byte("[General]\nName=DualSense Wireless Controller\nAddressType=public\n"),
	Cache: []byte("[ServiceRecords]\n0x00010000=35760900000A00010000\n"),
}

// playerTwo is a second paired controller with no cache entry yet.
var playerTwo = bonds.Files{Info: []byte("[General]\nName=Player Two\nAddressType=random\n")}

func TestPersistCreatesTheSecretOfANewBond(t *testing.T) {
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
	if secret.Metadata.Labels[bonds.AdapterLabel] != "14-b4-57-91-2f-c8" {
		t.Errorf("labels = %+v", secret.Metadata.Labels)
	}
	if len(secret.Metadata.OwnerReferences) != 1 ||
		secret.Metadata.OwnerReferences[0].Kind != peripheralKind ||
		secret.Metadata.OwnerReferences[0].Name != "a0-ab-51-33-b7-12" {
		t.Errorf("ownerReferences = %+v", secret.Metadata.OwnerReferences)
	}
	if got := string(secret.Data["a0-ab-51-33-b7-12.info"]); got != string(oneBond.Info) {
		t.Errorf("the info file arrived as %q", got)
	}
	if got := string(secret.Data["a0-ab-51-33-b7-12.cache"]); got != string(oneBond.Cache) {
		t.Errorf("the cache entry arrived as %q", got)
	}
	want := []string{"GET " + testSecretPath, "POST " + testSecretsPath}
	if len(fixture.requests) != 2 || fixture.requests[0] != want[0] || fixture.requests[1] != want[1] {
		t.Errorf("requests = %v, want %v", fixture.requests, want)
	}
}

func TestPersistCreatesNothingForAnAdapterWithNoBonds(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, nil))

	if !store.persist(adapterIs(t), nil, nil) {
		t.Fatal("an adapter with no bonds reported a bond unstored")
	}
	if fixture.created != nil {
		t.Fatalf("an empty tree created a Secret: %+v", fixture.created)
	}
}

// BlueZ writes the cache entry after the info file, so a later pass
// must copy it with no other change to the bond. A BR/EDR HID device
// restored from the info file alone connects and then drops, because
// the input profile finds no HID SDP record.
func TestPersistWritesACacheEntryThatAppearsAfterThePairing(t *testing.T) {
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
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q", fixture.updated.Metadata.ResourceVersion)
	}
}

// The adapter's cache directory has an entry for every device whose
// name the radio resolved, including nearby phones. Nothing about a
// device with no directory of its own may reach the API server.
func TestPersistWritesNoCacheEntryForAnUnpairedDevice(t *testing.T) {
	root := bondTree(t, map[string]bonds.Files{testDevice: oneBond})
	neighbour := filepath.Join(root, testAdapter, "cache", "E3:28:E9:23:21:6F")
	if err := os.WriteFile(neighbour, []byte("[General]\nName=Somebody's Phone\n"), 0o600); err != nil {
		t.Fatalf("writing the cache entry of the neighbour: %v", err)
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
			t.Errorf("the Secret holds %s, a device this adapter never paired with: %q", key, value)
		}
	}
}

func TestPersistWritesNothingWhenTheBondMatchesTheSecret(t *testing.T) {
	// bluetoothd rewrites its tree without changing any key, and the
	// backstop pass runs with no event, so a write on every pass would
	// send a request each time.
	fixture := &bondSecretFixture{existing: storedBond(t, testDevice, oneBond)}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("persist returned false for an adapter whose bonds match their Secrets, want true")
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Fatalf("persist wrote to the API for an adapter whose bonds match their Secrets: %v, want no write", fixture.requests)
	}
}

func TestPersistSkipsABondWithNoPeripheral(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if store.persist(adapterIs(t), nil, nil) {
		t.Fatal("a bond with no Peripheral reported the bond stored")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a bond with no Peripheral reached the API: %v", fixture.requests)
	}
}

// The Secret of a bond that is being unpaired is deleted with its
// Peripheral, so it is not written again.
func TestPersistLeavesABondBeingUnpairedAlone(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	unpairing := map[bonds.Address]bool{testAddress(t, testDevice): true}

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), unpairing) {
		t.Fatal("a bond being unpaired reported a bond unstored")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a bond being unpaired reached the API: %v", fixture.requests)
	}
}

// persist does not empty or delete the Secret of a device that has left
// the tree. The Secret is deleted with its Peripheral.
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
	// Until bluetoothd publishes its adapter, the store cannot tell
	// which radio the bonds on disk belong to.
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
	fixture := &bondSecretFixture{writeStatus: http.StatusInternalServerError}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))

	if store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("a failed write reported the bond stored")
	}
}

// The store logs the older per-adapter Secret once and does not
// modify it.
func TestPersistReportsTheOlderPerAdapterSecretAndKeepsIt(t *testing.T) {
	fixture := &bondSecretFixture{existing: map[string]*bonds.Secret{
		testOlderSecretPath: {Metadata: bonds.SecretMeta{Name: "bluetooth-bonds-14-b4-57-91-2f-c8"}},
	}}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.reportedOlderSecret = false

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the pass reported the bond unstored")
	}
	if !store.reportedOlderSecret {
		t.Error("the older Secret was not reported")
	}
	for _, request := range fixture.requests {
		if strings.HasSuffix(request, testOlderSecretPath) && !strings.HasPrefix(request, "GET ") {
			t.Errorf("persist wrote to the older per-adapter Secret: %v, want it unchanged", fixture.requests)
		}
	}

	fixture.requests = nil
	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the second pass reported the bond unstored")
	}
	for _, request := range fixture.requests {
		if strings.HasSuffix(request, testOlderSecretPath) {
			t.Errorf("the older Secret was read again: %v", fixture.requests)
		}
	}
}

// The next pod creates the controller's virtual nodes from the stored
// snapshot before the controller connects again.
func TestPersistWritesTheEvdevSnapshot(t *testing.T) {
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.relays.restore("a0:ab:51:33:b7:12", storedCapabilities(t))

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the pass reported the bond unstored")
	}

	stored := fixture.created.Snapshot(testAddress(t, testDevice))
	var document evdevSnapshot
	if err := json.Unmarshal(stored, &document); err != nil {
		t.Fatalf("the Secret holds %q: %v", stored, err)
	}
	if len(document.Nodes) != 1 || document.Nodes[0].Name != "Wireless Controller" {
		t.Errorf("the stored snapshot = %+v", document)
	}
}

// The loop wakes on every uevent, so a rewrite of a bond that already
// matches would send a request for every button press.
func TestPersistWritesNothingWhenTheSnapshotAlreadyAgrees(t *testing.T) {
	snapshot := storedCapabilities(t)
	address := testAddress(t, testDevice)
	secret := bonds.NewBondSecret("liken-system", testAdapterAddress(t), address, oneBond, snapshot,
		bondOwner(testPeripheralOwner()))
	secret.Metadata.ResourceVersion = "7"
	fixture := &bondSecretFixture{
		existing: map[string]*bonds.Secret{bonds.BondSecretPath("liken-system", address): secret},
	}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{testDevice: oneBond}))
	store.relays.restore("a0:ab:51:33:b7:12", snapshot)

	if !store.persist(adapterIs(t), ownedBy(t, testDevice), nil) {
		t.Fatal("the pass reported the bond unstored")
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Errorf("a bond that already agrees was written again: %+v %+v", fixture.created, fixture.updated)
	}
}

// The stored snapshots create the virtual devices of every bonded
// controller at startup, so a claim can be prepared before any
// controller connects.
func TestRestoreCreatesTheVirtualDevicesFromTheStoredSnapshots(t *testing.T) {
	list := bonds.SecretList{Items: []bonds.Secret{
		*bonds.NewBondSecret("liken-system", testAdapterAddress(t), testAddress(t, testDevice),
			oneBond, storedCapabilities(t), bondOwner(testPeripheralOwner())),
	}}
	requested := ""
	store := &bondStore{
		client: testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requested = r.URL.Path + "?" + r.URL.RawQuery
			writeJSON(t, w, list)
		})),
		namespace: "liken-system",
		relays:    relaysFor(t),
	}

	store.restore(adapterIs(t))

	if nodes := store.relays.virtualNodes("a0:ab:51:33:b7:12"); len(nodes) != 1 {
		t.Fatalf("virtual nodes = %v, want one", nodes)
	}
	// The adapter label keeps another radio's bonds out of this pod's
	// relays.
	want := testSecretsPath + "?labelSelector=bluetooth.liken.sh%2Fadapter%3D14-b4-57-91-2f-c8"
	if requested != want {
		t.Errorf("listed %q, want %q", requested, want)
	}
}
