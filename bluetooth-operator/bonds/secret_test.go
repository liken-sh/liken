package bonds

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestBondSecretNameCarriesTheDevice(t *testing.T) {
	// One Secret per bond, named after the device and not after the
	// node or the pod, so the keys follow the hardware they belong to.
	if got := BondSecretName(address(t, testDevice)); got != "bluetooth-bond-7c-66-ef-22-e7-80" {
		t.Errorf("BondSecretName = %q", got)
	}
}

func TestSecretNameStillReadsTheOlderPerAdapterName(t *testing.T) {
	// Nothing writes this name now. The operator reports the object so
	// that a person deletes it, which takes the name.
	if got := SecretName(address(t, testAdapter)); got != "bluetooth-bonds-04-4a-69-66-92-27" {
		t.Errorf("SecretName = %q", got)
	}
}

func TestBondSecretPathNamesTheOperatorsNamespace(t *testing.T) {
	want := "/api/v1/namespaces/bluetooth/secrets/bluetooth-bond-7c-66-ef-22-e7-80"
	if got := BondSecretPath("bluetooth", address(t, testDevice)); got != want {
		t.Errorf("BondSecretPath = %q, want %q", got, want)
	}
}

// The label gathers one radio's bonds, because the init container
// runs before bluetoothd and has no list of device addresses.
func TestAdapterSelectorNamesTheRadio(t *testing.T) {
	want := "bluetooth.liken.sh/adapter=04-4a-69-66-92-27"
	if got := AdapterSelector(address(t, testAdapter)); got != want {
		t.Errorf("AdapterSelector = %q, want %q", got, want)
	}
}

// A Secret that holds one bond and one that holds a whole adapter are
// told apart by name, so the init container can apply the older
// layout first and let the current one win.
func TestOneBondReadsTheLayoutFromTheName(t *testing.T) {
	perBond := Secret{Metadata: SecretMeta{Name: BondSecretName(address(t, testDevice))}}
	perAdapter := Secret{Metadata: SecretMeta{Name: SecretName(address(t, testAdapter))}}
	if !perBond.OneBond() {
		t.Errorf("%q did not read as one bond", perBond.Metadata.Name)
	}
	if perAdapter.OneBond() {
		t.Errorf("%q read as one bond", perAdapter.Metadata.Name)
	}
}

func TestNewBondSecretHoldsTheDevicesFiles(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), testOwner)

	if secret.Metadata.Name != "bluetooth-bond-7c-66-ef-22-e7-80" {
		t.Errorf("name = %q", secret.Metadata.Name)
	}
	if secret.Metadata.Namespace != "bluetooth" {
		t.Errorf("namespace = %q", secret.Metadata.Namespace)
	}
	if secret.Type != "Opaque" {
		t.Errorf("type = %q", secret.Type)
	}
	want := map[string]string{
		"app.kubernetes.io/name":     "bluetooth-operator",
		"bluetooth.liken.sh/adapter": "04-4a-69-66-92-27",
	}
	if !reflect.DeepEqual(secret.Metadata.Labels, want) {
		t.Errorf("labels = %v, want %v", secret.Metadata.Labels, want)
	}
	// The Pairing owns the Secret, so deleting the Pairing collects the
	// keys and no bond outlives the object that names it.
	if !reflect.DeepEqual(secret.Metadata.OwnerReferences, []Owner{testOwner}) {
		t.Errorf("ownerReferences = %+v", secret.Metadata.OwnerReferences)
	}
	// Each key is the device's address in the one form a Secret key
	// accepts, a colon is not legal in one, and the suffix names which
	// of the device's two files the value holds.
	if got := string(secret.Data["7c-66-ef-22-e7-80.info"]); got != testInfo {
		t.Errorf("data[7c-66-ef-22-e7-80.info] = %q", got)
	}
	if got := string(secret.Data["7c-66-ef-22-e7-80.cache"]); got != testCache {
		t.Errorf("data[7c-66-ef-22-e7-80.cache] = %q", got)
	}
}

// A device paired before its cache entry was written has one file, and
// the Secret holds one key for it. An empty value would restore an
// empty cache file, which is not the same as no cache file.
func TestNewBondSecretWritesNoCacheKeyWithoutOne(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, ""), testOwner)

	if _, found := secret.Data["7c-66-ef-22-e7-80.cache"]; found {
		t.Errorf("data = %v, want no cache key", secret.Data)
	}
}

// A Secret's data is base64 on the wire. encoding/json does that for
// []byte, so an info file with any byte in it survives the round trip.
func TestSecretDataIsBase64OnTheWire(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), testOwner)

	body, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var wire struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(wire.Data["7c-66-ef-22-e7-80.info"])
	if err != nil {
		t.Fatalf("the value is not base64: %v", err)
	}
	if string(decoded) != testInfo {
		t.Errorf("decoded = %q", decoded)
	}

	var read Secret
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	device := read.Tree()[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q", device.Info)
	}
	if string(device.Cache) != testCache {
		t.Errorf("cache = %q", device.Cache)
	}
}

// A key that is not an address does not name a device. Skipping it lets
// bluetoothd start with the bonds that are readable, where failing the
// whole read would start it with none.
func TestSecretTreeSkipsKeysThatAreNotAddresses(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80.info": []byte(testInfo),
		"settings":               []byte("[General]\n"),
		"settings.info":          []byte("[General]\n"),
		".cache":                 []byte("[General]\n"),
		"":                       []byte("nothing"),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	if got := string(tree[address(t, testDevice)].Info); got != testInfo {
		t.Errorf("info = %q", got)
	}
}

// Some Secrets in the field hold one key for each device, the bare
// address, holding the info file. Reading that layout costs a few
// lines here; the alternative is a person who recreates the Secret by
// pairing the controller again.
func TestSecretTreeReadsTheBareAddressLayout(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80": []byte(testInfo),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	device := tree[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q", device.Info)
	}
	// The bare-address layout stored no cache file, so the restored
	// device has none, and bluetoothd writes one at the next discovery.
	if len(device.Cache) != 0 {
		t.Errorf("cache = %q, want none", device.Cache)
	}
}

// The operator rewrites the Secret on the first pass that finds a
// difference, and the write replaces the whole object, so both layouts
// coexist only until then, and the current keys win while they do.
func TestSecretTreeTakesTheCurrentLayoutOverTheBareAddress(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80":       []byte("[LinkKey]\nKey=FF\n"),
		"7c-66-ef-22-e7-80.info":  []byte(testInfo),
		"7c-66-ef-22-e7-80.cache": []byte(testCache),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	device := tree[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q", device.Info)
	}
	if string(device.Cache) != testCache {
		t.Errorf("cache = %q", device.Cache)
	}
}

// A cache key with no info key beside it names a device this adapter
// holds no bond with. Restoring it would put an entry into BlueZ's
// cache directory for hardware the machine has no claim on.
func TestSecretTreeSkipsACacheKeyWithNoBond(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80.info":  []byte(testInfo),
		"e3-28-e9-23-21-6f.cache": []byte(testNeighbourCache),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	if _, found := tree[address(t, testNeighbour)]; found {
		t.Errorf("a device with no info file reached the tree: %v", tree)
	}
}

// One round trip through the API, so that what the operator reads off
// one machine is what the init container writes on the next.
func TestSecretTreeReturnsWhatNewBondSecretStored(t *testing.T) {
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}

	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		tree[address(t, testDevice)], testOwner)
	if again := secret.Tree(); !equalTrees(tree, again) {
		t.Fatalf("the tree changed on the way through a Secret: %v, want %v", again, tree)
	}
}

// The init container gathers one radio's bonds out of several Secrets,
// so the merge is what rebuilds the tree bluetoothd reads.
func TestMergeGathersEveryBond(t *testing.T) {
	one := Tree{address(t, testDevice): files(testInfo, testCache)}
	two := Tree{address(t, testNeighbour): files(testInfo, "")}

	one.Merge(two)

	if len(one) != 2 {
		t.Fatalf("got %d bonds, want 2: %v", len(one), one)
	}
}

// testOwner is the Pairing that owns a bond's Secret.
var testOwner = Owner{
	APIVersion: "bluetooth.liken.sh/v1alpha1",
	Kind:       "Pairing",
	Name:       "7c-66-ef-22-e7-80",
	UID:        "8f0f0b32-0000-4000-8000-000000000001",
}
