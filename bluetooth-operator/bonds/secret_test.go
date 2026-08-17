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
	tree := Tree{address(t, testDevice): []byte(testInfo)}

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
	// The key is the device's address in the one form a Secret key
	// accepts: a colon is not legal in one.
	if got := string(secret.Data["7c-66-ef-22-e7-80"]); got != testInfo {
		t.Errorf("data[7c-66-ef-22-e7-80] = %q", got)
	}
}

// A Secret's data is base64 on the wire. encoding/json does that for
// []byte, so an info file with any byte in it survives the round trip.
func TestSecretDataIsBase64OnTheWire(t *testing.T) {
	secret := NewSecret("bluetooth", address(t, testAdapter), Tree{address(t, testDevice): []byte(testInfo)})

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
	decoded, err := base64.StdEncoding.DecodeString(wire.Data["7c-66-ef-22-e7-80"])
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
	if got := string(read.Tree()[address(t, testDevice)]); got != testInfo {
		t.Errorf("info = %q", got)
	}
}

// A key that is not an address names no device. Skipping it lets
// bluetoothd start with the bonds that are readable, where failing the
// whole read would start it with none.
func TestSecretTreeSkipsKeysThatAreNotAddresses(t *testing.T) {
	secret := Secret{Data: map[string][]byte{
		"7c-66-ef-22-e7-80": []byte(testInfo),
		"settings":          []byte("[General]\n"),
		"":                  []byte("nothing"),
	}}

	tree := secret.Tree()
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	if got := string(tree[address(t, testDevice)]); got != testInfo {
		t.Errorf("info = %q", got)
	}
}
