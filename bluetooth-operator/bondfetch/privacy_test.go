package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// The paths bondfetch reads for the test radio: its Adapter, and the
// Secret that holds its identity file.
const (
	testAdapterPath  = "/apis/bluetooth.liken.sh/v1alpha1/adapters/04-4a-69-66-92-27"
	testIdentityPath = "/api/v1/namespaces/bluetooth/secrets/bluetooth-identity-04-4a-69-66-92-27"
	testIdentity     = "[General]\nIdentityResolvingKey=0123456789ABCDEF0123456789ABCDEF\n"
)

// apiObjects serves each object at its path, and answers 404 for any
// other path, the way the API server answers for an object that does
// not exist.
func apiObjects(t *testing.T, objects map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		object, found := objects[r.URL.Path]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	})
}

// failingAPI answers every request with 500.
func failingAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
}

// readSetting answers the privacy file bondfetch wrote.
func readSetting(t *testing.T, settings string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(settings, "privacy"))
	if err != nil {
		t.Fatalf("reading the privacy file: %v", err)
	}
	return string(contents)
}

// The file holds the value of spec.privacy, and an empty field is off.
// An Adapter that does not exist yet is off too, because the operator
// creates it on its first pass.
func TestWritePrivacyWritesTheFieldsValue(t *testing.T) {
	cases := []struct {
		name    string
		objects map[string]any
		want    string
	}{
		{name: "device", objects: map[string]any{testAdapterPath: map[string]any{"spec": map[string]any{"privacy": "device"}}}, want: "device\n"},
		{name: "limited-network", objects: map[string]any{testAdapterPath: map[string]any{"spec": map[string]any{"privacy": "limited-network"}}}, want: "limited-network\n"},
		{name: "an empty field", objects: map[string]any{testAdapterPath: map[string]any{"spec": map[string]any{}}}, want: "off\n"},
		{name: "no Adapter", objects: map[string]any{}, want: "off\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := testAPI(t, apiObjects(t, c.objects))
			settings := t.TempDir()

			if err := writePrivacy(api, testAddress, settings); err != nil {
				t.Fatalf("writePrivacy: %v", err)
			}

			if got := readSetting(t, settings); got != c.want {
				t.Errorf("privacy = %q, want %q", got, c.want)
			}
		})
	}
}

// A read that fails is not an Adapter with privacy off. bluetoothd must
// not start with a value that a person did not choose.
func TestWritePrivacyFailsWhenTheAdapterCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())
	settings := t.TempDir()

	if err := writePrivacy(api, testAddress, settings); err == nil {
		t.Fatal("writePrivacy reported success for an Adapter it could not read")
	}
	if _, err := os.Stat(filepath.Join(settings, "privacy")); !os.IsNotExist(err) {
		t.Errorf("writePrivacy wrote a value after a failed read")
	}
}

// The stored identity file goes back where bluetoothd reads it, so the
// radio presents the identity its peers already know.
func TestRestoreIdentityWritesTheStoredFile(t *testing.T) {
	secret := bonds.NewIdentitySecret("bluetooth", testAddress, []byte(testIdentity), bonds.Owner{})
	api := testAPI(t, apiObjects(t, map[string]any{testIdentityPath: secret}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("restoreIdentity: %v", err)
	}

	identity, err := os.ReadFile(filepath.Join(root, "04:4A:69:66:92:27", "identity"))
	if err != nil {
		t.Fatalf("reading the identity file: %v", err)
	}
	if string(identity) != testIdentity {
		t.Errorf("identity = %q, want %q", identity, testIdentity)
	}
}

// A radio that never had privacy on has no identity Secret, and gets no
// file.
func TestRestoreIdentityWritesNothingWithNoSecret(t *testing.T) {
	api := testAPI(t, apiObjects(t, map[string]any{}))
	root := t.TempDir()

	if err := restoreIdentity(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("restoreIdentity: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("it wrote %v for a radio with no identity Secret", entries)
	}
}

// A read that fails is not a radio with no identity. bluetoothd would
// write a new key, and every bonded peer would hold a key for an
// identity the radio does not present.
func TestRestoreIdentityFailsWhenTheSecretCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())

	if err := restoreIdentity(api, "bluetooth", testAddress, t.TempDir()); err == nil {
		t.Fatal("restoreIdentity reported success for a Secret it could not read")
	}
}
