package main

// These tests cover the bond store's outcomes against a test API
// server and a BlueZ storage tree on disk: create the Secret at the
// first pairing, update it when a bond changes, write nothing when the
// two already agree, empty it when the last device is unpaired, and
// refuse to empty it when the tree is not there to read.

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

// testSecretPath is where one adapter's bonds live in the API, and
// testSecretsPath is the collection a create goes to.
const (
	testSecretPath  = "/api/v1/namespaces/liken-system/secrets/bluetooth-bonds-14-b4-57-91-2f-c8"
	testSecretsPath = "/api/v1/namespaces/liken-system/secrets"
)

func testAdapterAddress(t *testing.T) bonds.Address {
	t.Helper()
	address, err := bonds.ParseAddress(testAdapter)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

// bondSecretFixture is a small API server that holds at most one
// Secret. It remembers the requests it received, and writeStatus makes
// every write fail.
type bondSecretFixture struct {
	existing    *bonds.Secret
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
			if f.existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(f.existing)
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

// storedSecret is one adapter's bonds as the API server already holds
// them, with the resourceVersion a read carries.
func storedSecret(t *testing.T, devices map[string]bonds.Files) *bonds.Secret {
	t.Helper()
	secret := bonds.NewSecret("liken-system", testAdapterAddress(t), testTree(t, devices))
	secret.Metadata.ResourceVersion = "7"
	return secret
}

// testTree builds one adapter's bonds out of device addresses and
// their files.
func testTree(t *testing.T, devices map[string]bonds.Files) bonds.Tree {
	t.Helper()
	tree := bonds.Tree{}
	for address, files := range devices {
		device, err := bonds.ParseAddress(address)
		if err != nil {
			t.Fatal(err)
		}
		tree[device] = files
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
// storage tree the test owns.
func testBondStore(t *testing.T, fixture *bondSecretFixture, root string) *bondStore {
	t.Helper()
	return &bondStore{
		client:    testClient(t, fixture.handler(t)),
		namespace: "liken-system",
		root:      root,
	}
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
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))

	if !store.persist(adapterIs(t)) {
		t.Fatal("the first pairing reported the Secret unpersisted")
	}
	secret := fixture.created
	if secret == nil {
		t.Fatal("no Secret was created")
	}
	if secret.Metadata.Name != "bluetooth-bonds-14-b4-57-91-2f-c8" {
		t.Errorf("name = %q", secret.Metadata.Name)
	}
	if secret.Type != bonds.SecretType {
		t.Errorf("type = %q", secret.Type)
	}
	if secret.Metadata.Labels["bluetooth.liken.sh/adapter"] != "14-b4-57-91-2f-c8" {
		t.Errorf("labels = %+v", secret.Metadata.Labels)
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

	if !store.persist(adapterIs(t)) {
		t.Fatal("an adapter with no bonds reported the Secret unpersisted")
	}
	if fixture.created != nil {
		t.Fatalf("an empty tree created a Secret: %+v", fixture.created)
	}
}

func TestPersistUpdatesTheSecretWhenAPairingLandsOnDisk(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedSecret(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	})}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
		"B4:8C:9D:11:22:33": playerTwo,
	}))

	if !store.persist(adapterIs(t)) {
		t.Fatal("a new pairing reported the Secret unpersisted")
	}
	if fixture.updated == nil {
		t.Fatal("the new pairing was not written")
	}
	// Two devices, and the count is of devices and not of keys: a
	// device carries one key or two, depending on whether bluetoothd
	// has written its cache entry.
	if got := len(fixture.updated.Tree()); got != 2 {
		t.Errorf("the Secret holds %d devices, want 2: %+v", got, fixture.updated.Data)
	}
	// The write carries the resourceVersion from the read, so a second
	// writer gets a conflict instead of losing the first writer's bonds.
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q", fixture.updated.Metadata.ResourceVersion)
	}
}

// A device pairs first and gets its cache entry when bluetoothd
// resolves its name, so the two files can land on different passes.
// The second file has to reach the Secret on its own: a BR/EDR HID
// device restored from an info file alone connects and drops again,
// because the input profile finds no HID SDP record.
func TestPersistCarriesACacheEntryThatLandsAfterThePairing(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedSecret(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": {Info: oneBond.Info},
	})}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))

	if !store.persist(adapterIs(t)) {
		t.Fatal("a new cache entry reported the Secret unpersisted")
	}
	if fixture.updated == nil {
		t.Fatal("the new cache entry was not written")
	}
	if got := string(fixture.updated.Data["a0-ab-51-33-b7-12.cache"]); got != string(oneBond.Cache) {
		t.Errorf("the cache entry arrived as %q", got)
	}
}

// The adapter's cache directory holds one entry for every device the
// radio has resolved a name for, which at 44 Stony Point is the
// neighbours' phones. A device with no directory of its own is not
// paired to this adapter, and nothing about it may reach the API.
func TestPersistCarriesNoCacheEntryForADeviceThatIsNotPaired(t *testing.T) {
	root := bondTree(t, map[string]bonds.Files{"A0:AB:51:33:B7:12": oneBond})
	neighbour := filepath.Join(root, testAdapter, "cache", "E3:28:E9:23:21:6F")
	if err := os.WriteFile(neighbour, []byte("[General]\nName=Somebody's Phone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, root)

	if !store.persist(adapterIs(t)) {
		t.Fatal("the first pairing reported the Secret unpersisted")
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

func TestPersistWritesNothingWhenTheTreeMatchesTheSecret(t *testing.T) {
	// bluetoothd rewrites its tree for reasons that change no key, and
	// the loop passes once a minute with no event at all. A write on
	// every pass would be a write to the API server on every pass.
	devices := map[string]bonds.Files{"A0:AB:51:33:B7:12": oneBond}
	fixture := &bondSecretFixture{existing: storedSecret(t, devices)}
	store := testBondStore(t, fixture, bondTree(t, devices))

	if !store.persist(adapterIs(t)) {
		t.Fatal("a steady adapter reported the Secret unpersisted")
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Fatalf("a steady adapter wrote to the API: %v", fixture.requests)
	}
}

func TestPersistDropsAnUnpairedDevice(t *testing.T) {
	fixture := &bondSecretFixture{existing: storedSecret(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
		"B4:8C:9D:11:22:33": playerTwo,
	})}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))

	if !store.persist(adapterIs(t)) {
		t.Fatal("an unpairing reported the Secret unpersisted")
	}
	if fixture.updated == nil {
		t.Fatal("the unpairing was not written")
	}
	if len(fixture.updated.Tree()) != 1 || fixture.updated.Data["a0-ab-51-33-b7-12.info"] == nil {
		t.Errorf("data = %+v", fixture.updated.Data)
	}
}

func TestPersistEmptiesTheSecretWhenTheLastDeviceIsUnpaired(t *testing.T) {
	// The adapter's own directory is there and holds no device, which
	// is bluetoothd's own answer that nothing is paired any more.
	fixture := &bondSecretFixture{existing: storedSecret(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	})}
	store := testBondStore(t, fixture, bondTree(t, nil))

	if !store.persist(adapterIs(t)) {
		t.Fatal("the last unpairing reported the Secret unpersisted")
	}
	if fixture.updated == nil {
		t.Fatal("the last unpairing was not written")
	}
	if len(fixture.updated.Data) != 0 {
		t.Errorf("data = %+v, want none", fixture.updated.Data)
	}
}

func TestPersistNeverErasesBondsWhenTheTreeIsGone(t *testing.T) {
	// The adapter has no directory under the root. bonds.ReadTree
	// answers with an empty tree, and so does an adapter that paired
	// nothing, so the directory is what tells a real unpairing from a
	// volume that is not mounted or a tree that a different adapter
	// wrote. Only the first of those may empty a Secret.
	fixture := &bondSecretFixture{existing: storedSecret(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	})}
	store := testBondStore(t, fixture, t.TempDir())

	if store.persist(adapterIs(t)) {
		t.Fatal("a tree that is not there reported the Secret persisted")
	}
	if fixture.updated != nil {
		t.Fatalf("a tree that is not there erased the stored bonds: %+v", fixture.updated)
	}
}

func TestPersistReadsTheAdapterAddressOnce(t *testing.T) {
	// The pod's bondfetch restored one radio's keys, and the operator
	// persists that radio's tree for the life of the pod. A second read
	// could answer with a different adapter, whose Secret this pod
	// never restored and whose tree is therefore not on this disk.
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))
	reads := 0
	address := testAdapterAddress(t)
	read := func() (bonds.Address, error) {
		reads++
		return address, nil
	}

	if !store.persist(read) || !store.persist(read) {
		t.Fatal("a pass reported the Secret unpersisted")
	}
	if reads != 1 {
		t.Fatalf("the address was read %d times, want 1", reads)
	}
}

func TestPersistWaitsForBlueZToPublishAnAdapter(t *testing.T) {
	// bluetoothd publishes its object tree a moment after it claims its
	// bus name. Until it does, the operator does not know which Secret
	// the bonds belong in.
	fixture := &bondSecretFixture{}
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))
	read := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }

	if store.persist(read) {
		t.Fatal("a pass with no adapter reported the Secret persisted")
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
	store := testBondStore(t, fixture, bondTree(t, map[string]bonds.Files{
		"A0:AB:51:33:B7:12": oneBond,
	}))

	if store.persist(adapterIs(t)) {
		t.Fatal("a failed write reported the Secret persisted")
	}
}
