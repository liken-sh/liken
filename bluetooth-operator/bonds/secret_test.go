package bonds

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSecretNameCarriesTheAdapter(t *testing.T) {
	// One Secret per adapter, named after the adapter and not after
	// the node or the pod, so the keys follow the radio they belong to.
	if got := SecretName(address(t, testAdapter)); got != "bluetooth-bonds-04-4a-69-66-92-27" {
		t.Errorf("SecretName = %q", got)
	}
}

func TestSecretPathNamesTheOperatorsNamespace(t *testing.T) {
	want := "/api/v1/namespaces/bluetooth/secrets/bluetooth-bonds-04-4a-69-66-92-27"
	if got := SecretPath("bluetooth", address(t, testAdapter)); got != want {
		t.Errorf("SecretPath = %q, want %q", got, want)
	}
}

func TestNewSecretHoldsEveryBond(t *testing.T) {
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}

	secret := NewSecret("bluetooth", address(t, testAdapter), tree)

	if secret.Metadata.Name != "bluetooth-bonds-04-4a-69-66-92-27" {
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
// the Secret carries one key for it. An empty value would restore an
// empty cache file, which is not the same as no cache file.
func TestNewSecretWritesNoCacheKeyWithoutOne(t *testing.T) {
	tree := Tree{address(t, testDevice): files(testInfo, "")}

	secret := NewSecret("bluetooth", address(t, testAdapter), tree)

	if _, found := secret.Data["7c-66-ef-22-e7-80.cache"]; found {
		t.Errorf("data = %v, want no cache key", secret.Data)
	}
}

// A Secret's data is base64 on the wire. encoding/json does that for
// []byte, so an info file with any byte in it survives the round trip.
func TestSecretDataIsBase64OnTheWire(t *testing.T) {
	secret := NewSecret("bluetooth", address(t, testAdapter), Tree{address(t, testDevice): files(testInfo, testCache)})

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

// A key that is not an address names no device. Skipping it lets
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

// The first Secrets this operator wrote held one key for each device,
// the bare address, carrying the info file. One of them is in the
// field. Reading it costs a few lines here, where the alternative is
// a person who recreates the Secret by pairing the controller again.
func TestSecretTreeReadsTheFirstLayout(t *testing.T) {
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
	// The first layout stored no cache file, so the restored device
	// has none, and bluetoothd writes one at the next discovery.
	if len(device.Cache) != 0 {
		t.Errorf("cache = %q, want none", device.Cache)
	}
}

// The operator rewrites the Secret on the first pass that sees a
// difference, and the write replaces the whole object, so both layouts
// coexist only until then. The current keys win for as long as they do.
func TestSecretTreeTakesTheCurrentLayoutOverTheFirst(t *testing.T) {
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
func TestSecretTreeReturnsWhatNewSecretStored(t *testing.T) {
	tree := Tree{
		address(t, testDevice):    files(testInfo, testCache),
		address(t, testNeighbour): files(testInfo, ""),
	}

	if again := NewSecret("bluetooth", address(t, testAdapter), tree).Tree(); !tree.Same(again) {
		t.Fatalf("the tree changed on the way through a Secret: %v, want %v", again, tree)
	}
}
