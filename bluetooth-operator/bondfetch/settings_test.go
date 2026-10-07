package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// testAdapterPath is the test radio's Adapter, which bondfetch reads
// for the settings.
const testAdapterPath = "/apis/bluetooth.liken.sh/v1alpha1/adapters/04-4a-69-66-92-27"

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

// readSetting answers one file bondfetch wrote into the settings
// volume.
func readSetting(t *testing.T, settings, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(settings, name))
	if err != nil {
		t.Fatalf("reading the %s file: %v", name, err)
	}
	return string(contents)
}

// adapterWithSpec is the test radio's Adapter with spec.
func adapterWithSpec(spec map[string]any) map[string]any {
	return map[string]any{testAdapterPath: map[string]any{"spec": spec}}
}

// The privacy file holds the value of spec.privacy, and an empty field
// is off. An Adapter that does not exist yet is off too, because the
// operator creates it on its first pass.
func TestWriteSettingsWritesThePrivacy(t *testing.T) {
	cases := []struct {
		name    string
		objects map[string]any
		want    string
	}{
		{name: "device", objects: adapterWithSpec(map[string]any{"privacy": "device"}), want: "device\n"},
		{name: "limited-network", objects: adapterWithSpec(map[string]any{"privacy": "limited-network"}), want: "limited-network\n"},
		{name: "an empty field", objects: adapterWithSpec(map[string]any{}), want: "off\n"},
		{name: "no Adapter", objects: map[string]any{}, want: "off\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := testAPI(t, apiObjects(t, c.objects))
			settings := t.TempDir()

			if _, err := writeSettings(api, testAddress, settings); err != nil {
				t.Fatalf("writeSettings: %v", err)
			}

			if got := readSetting(t, settings, "privacy"); got != c.want {
				t.Errorf("privacy = %q, want %q", got, c.want)
			}
		})
	}
}

// The btmon file holds spec.btmon, and an empty field is false. An
// Adapter that does not exist yet is false too, so a new radio starts
// with the trace off.
func TestWriteSettingsWritesTheTraceSetting(t *testing.T) {
	cases := []struct {
		name    string
		objects map[string]any
		want    string
	}{
		{name: "true", objects: adapterWithSpec(map[string]any{"btmon": true}), want: "true\n"},
		{name: "false", objects: adapterWithSpec(map[string]any{"btmon": false}), want: "false\n"},
		{name: "an empty field", objects: adapterWithSpec(map[string]any{}), want: "false\n"},
		{name: "no Adapter", objects: map[string]any{}, want: "false\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := testAPI(t, apiObjects(t, c.objects))
			settings := t.TempDir()

			if _, err := writeSettings(api, testAddress, settings); err != nil {
				t.Fatalf("writeSettings: %v", err)
			}

			if got := readSetting(t, settings, "btmon"); got != c.want {
				t.Errorf("btmon = %q, want %q", got, c.want)
			}
		})
	}
}

// A read that fails is not an Adapter with every setting off.
// bluetoothd must not start with a value that a person did not choose,
// so bondfetch writes neither file.
func TestWriteSettingsFailsWhenTheAdapterCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())
	settings := t.TempDir()

	if _, err := writeSettings(api, testAddress, settings); err == nil {
		t.Fatal("writeSettings reported success for an Adapter it could not read")
	}
	entries, err := os.ReadDir(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("writeSettings wrote %v after a failed read", entries)
	}
}
