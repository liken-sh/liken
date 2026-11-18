package bonds

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestBondSecretNameContainsTheDeviceAddress(t *testing.T) {
	// The name has no node or pod name in it, so the bond does not
	// depend on which machine the radio is plugged into.
	if got := BondSecretName(address(t, testDevice)); got != "bluetooth-bond-7c-66-ef-22-e7-80" {
		t.Errorf("BondSecretName = %q, want bluetooth-bond-7c-66-ef-22-e7-80", got)
	}
}

func TestSecretNameIsTheOlderPerAdapterName(t *testing.T) {
	// The operator never creates this Secret. It reads the name only to
	// report a leftover one.
	if got := SecretName(address(t, testAdapter)); got != "bluetooth-bonds-04-4a-69-66-92-27" {
		t.Errorf("SecretName = %q, want bluetooth-bonds-04-4a-69-66-92-27", got)
	}
}

func TestBondSecretPathIsInTheGivenNamespace(t *testing.T) {
	want := "/api/v1/namespaces/bluetooth/secrets/bluetooth-bond-7c-66-ef-22-e7-80"
	if got := BondSecretPath("bluetooth", address(t, testDevice)); got != want {
		t.Errorf("BondSecretPath = %q, want %q", got, want)
	}
}

// The init container runs before bluetoothd, so it has no list of
// device addresses and lists the bonds by the adapter's label.
func TestAdapterSelectorMatchesTheAdapterLabel(t *testing.T) {
	want := "bluetooth.liken.sh/adapter=04-4a-69-66-92-27"
	if got := AdapterSelector(address(t, testAdapter)); got != want {
		t.Errorf("AdapterSelector = %q, want %q", got, want)
	}
}

// OneBond tells the layouts apart by name, so the init container can
// merge the older layout first and let the current one win.
func TestOneBondDependsOnTheSecretName(t *testing.T) {
	perBond := Secret{Metadata: SecretMeta{Name: BondSecretName(address(t, testDevice))}}
	perAdapter := Secret{Metadata: SecretMeta{Name: SecretName(address(t, testAdapter))}}
	if !perBond.OneBond() {
		t.Errorf("OneBond is false for %q, want true", perBond.Metadata.Name)
	}
	if perAdapter.OneBond() {
		t.Errorf("OneBond is true for %q, want false", perAdapter.Metadata.Name)
	}
}

func TestNewBondSecretHoldsTheDevicesFiles(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), nil, testOwner)

	if secret.Metadata.Name != "bluetooth-bond-7c-66-ef-22-e7-80" {
		t.Errorf("name = %q, want bluetooth-bond-7c-66-ef-22-e7-80", secret.Metadata.Name)
	}
	if secret.Metadata.Namespace != "bluetooth" {
		t.Errorf("namespace = %q, want bluetooth", secret.Metadata.Namespace)
	}
	if secret.Type != "Opaque" {
		t.Errorf("type = %q, want Opaque", secret.Type)
	}
	want := map[string]string{
		"app.kubernetes.io/name":     "bluetooth-operator",
		"bluetooth.liken.sh/adapter": "04-4a-69-66-92-27",
	}
	if !reflect.DeepEqual(secret.Metadata.Labels, want) {
		t.Errorf("labels = %v, want %v", secret.Metadata.Labels, want)
	}
	// The owner reference makes the garbage collector delete the Secret
	// with its Peripheral.
	if !reflect.DeepEqual(secret.Metadata.OwnerReferences, []Owner{testOwner}) {
		t.Errorf("ownerReferences = %+v, want %+v", secret.Metadata.OwnerReferences, []Owner{testOwner})
	}
	// Each key is the device address with dashes, because a colon is not
	// valid in a Secret key, plus a suffix for the file it holds.
	if got := string(secret.Data["7c-66-ef-22-e7-80.info"]); got != testInfo {
		t.Errorf("data[7c-66-ef-22-e7-80.info] = %q, want %q", got, testInfo)
	}
	if got := string(secret.Data["7c-66-ef-22-e7-80.cache"]); got != testCache {
		t.Errorf("data[7c-66-ef-22-e7-80.cache] = %q, want %q", got, testCache)
	}
}

// A device with no cache file gets only the info key, because an empty
// cache value would restore an empty cache file.
func TestNewBondSecretWritesNoCacheKeyWithoutOne(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, ""), nil, testOwner)

	if _, found := secret.Data["7c-66-ef-22-e7-80.cache"]; found {
		t.Errorf("data = %v, want no cache key", secret.Data)
	}
}

// The API sends Secret data as base64, which encoding/json does for
// []byte, so an info file with any bytes survives the round trip.
func TestSecretDataIsBase64OnTheWire(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), nil, testOwner)

	body, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshalling the Secret: %v", err)
	}
	var wire struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshalling the wire form: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(wire.Data["7c-66-ef-22-e7-80.info"])
	if err != nil {
		t.Fatalf("the value is not base64: %v", err)
	}
	if string(decoded) != testInfo {
		t.Errorf("decoded info = %q, want %q", decoded, testInfo)
	}

	var read Secret
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatalf("unmarshalling the Secret: %v", err)
	}
	device := read.Tree()[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q, want %q", device.Info, testInfo)
	}
	if string(device.Cache) != testCache {
		t.Errorf("cache = %q, want %q", device.Cache, testCache)
	}
}

// A key that is not a device's file is skipped, so bluetoothd starts
// with the readable bonds.
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
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
	if got := string(tree[address(t, testDevice)].Info); got != testInfo {
		t.Errorf("info = %q, want %q", got, testInfo)
	}
}

// Some existing Secrets store each device's info file under its bare
// address. Reading them saves pairing the controller again.
func TestSecretTreeReadsTheBareAddressLayout(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80": []byte(testInfo),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
	device := tree[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q, want %q", device.Info, testInfo)
	}
	// The bare-address layout has no cache file, so the restored device
	// has none and bluetoothd writes one at its next discovery.
	if len(device.Cache) != 0 {
		t.Errorf("cache = %q, want none", device.Cache)
	}
}

// When a Secret has both key forms for a device, the suffixed key wins.
func TestSecretTreeTakesTheCurrentLayoutOverTheBareAddress(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80":       []byte("[LinkKey]\nKey=FF\n"),
		"7c-66-ef-22-e7-80.info":  []byte(testInfo),
		"7c-66-ef-22-e7-80.cache": []byte(testCache),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
	device := tree[address(t, testDevice)]
	if string(device.Info) != testInfo {
		t.Errorf("info = %q, want %q", device.Info, testInfo)
	}
	if string(device.Cache) != testCache {
		t.Errorf("cache = %q, want %q", device.Cache, testCache)
	}
}

// A cache key with no info key is not a bond. Restoring it would put
// an entry in BlueZ's cache directory for a device that this adapter
// is not paired with.
func TestSecretTreeSkipsACacheKeyWithNoBond(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80.info":  []byte(testInfo),
		"e3-28-e9-23-21-6f.cache": []byte(testNeighbourCache),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
	if _, found := tree[address(t, testNeighbour)]; found {
		t.Errorf("the tree has an entry for a device with no info file: %v, want none", tree)
	}
}

// A tree stored in a Secret and read back is unchanged, so the tree
// that the operator stores from one machine is the tree that the init
// container writes on the next.
func TestSecretTreeReturnsWhatNewBondSecretStored(t *testing.T) {
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}

	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		tree[address(t, testDevice)], nil, testOwner)
	if again := secret.Tree(); !equalTrees(tree, again) {
		t.Fatalf("the tree read back from the Secret is %v, want %v", again, tree)
	}
}

// The init container reads one radio's bonds from several Secrets and
// merges them into the tree that bluetoothd reads.
func TestMergeGathersEveryBond(t *testing.T) {
	one := Tree{address(t, testDevice): files(testInfo, testCache)}
	two := Tree{address(t, testNeighbour): files(testInfo, "")}

	one.Merge(two)

	if len(one) != 2 {
		t.Fatalf("the tree has %d bonds, want 2: %v", len(one), one)
	}
}

// testOwner is the Peripheral that owns a bond's Secret.
var testOwner = Owner{
	APIVersion: "bluetooth.liken.sh/v1alpha1",
	Kind:       "Peripheral",
	Name:       "7c-66-ef-22-e7-80",
	UID:        "8f0f0b32-0000-4000-8000-000000000001",
}

// The snapshot is stored in the bond's Secret under its own key. Tree
// ignores it, because it is not a BlueZ file and the tree that the init
// container writes belongs to BlueZ.
func TestNewBondSecretHoldsTheEvdevSnapshot(t *testing.T) {
	snapshot := []byte(`{"version":1,"nodes":[{"name":"Wireless Controller"}]}`)
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), snapshot, testOwner)

	if got := string(secret.Data["7c-66-ef-22-e7-80.evdev"]); got != string(snapshot) {
		t.Errorf("data[7c-66-ef-22-e7-80.evdev] = %q, want %q", got, snapshot)
	}
	if got := string(secret.Snapshot(address(t, testDevice))); got != string(snapshot) {
		t.Errorf("Snapshot = %q, want %q", got, snapshot)
	}
	if got := string(secret.Snapshots()[address(t, testDevice)]); got != string(snapshot) {
		t.Errorf("Snapshots = %q, want %q", got, snapshot)
	}
	if got := secret.Tree()[address(t, testDevice)]; !got.Equal(files(testInfo, testCache)) {
		t.Errorf("the tree read back is %+v, want the info and cache files", got)
	}
}

// A controller that has never connected has no snapshot, and its key
// is absent and not empty.
func TestNewBondSecretWritesNoEvdevKeyWithoutASnapshot(t *testing.T) {
	secret := NewBondSecret("bluetooth", address(t, testAdapter), address(t, testDevice),
		files(testInfo, testCache), nil, testOwner)

	if _, found := secret.Data["7c-66-ef-22-e7-80.evdev"]; found {
		t.Errorf("data = %v, want no evdev key", secret.Data)
	}
	if len(secret.Snapshots()) != 0 {
		t.Errorf("Snapshots = %v, want none", secret.Snapshots())
	}
}
