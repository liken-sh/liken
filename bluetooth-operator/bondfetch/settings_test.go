package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const testAdapterPath = "/apis/bluetooth.liken.sh/v1alpha1/adapters/04-4a-69-66-92-27"

// apiObjects serves each object as JSON at its path, and returns 404
// for any other path, as the API server does for a missing object.
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
			t.Fatalf("encoding the %s object: %v", r.URL.Path, err)
		}
		_, _ = w.Write(body)
	})
}

func failingAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
}

func readSetting(t *testing.T, settings, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(settings, name))
	if err != nil {
		t.Fatalf("reading the %s file: %v", name, err)
	}
	return string(contents)
}

func adapterWithSpec(spec map[string]any) map[string]any {
	return map[string]any{testAdapterPath: map[string]any{"spec": spec}}
}

// The privacy file holds spec.privacy. An empty field means off, and so
// does a missing Adapter, because the operator creates the Adapter on
// its first pass.
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
				t.Fatalf("writing the settings: %v", err)
			}

			if got := readSetting(t, settings, "privacy"); got != c.want {
				t.Errorf("privacy = %q, want %q", got, c.want)
			}
		})
	}
}

// The btmon file holds spec.btmon. An empty field means false, and so
// does a missing Adapter, so a new radio starts with the trace off.
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
				t.Fatalf("writing the settings: %v", err)
			}

			if got := readSetting(t, settings, "btmon"); got != c.want {
				t.Errorf("btmon = %q, want %q", got, c.want)
			}
		})
	}
}

// A failed read is different from a missing Adapter. bondfetch writes
// neither file and returns an error, so bluetoothd does not start with
// a value that nobody chose.
func TestWriteSettingsFailsWhenTheAdapterCannotBeRead(t *testing.T) {
	api := testAPI(t, failingAPI())
	settings := t.TempDir()

	if _, err := writeSettings(api, testAddress, settings); err == nil {
		t.Fatal("writeSettings returned no error after the API server refused to return the Adapter, want an error")
	}
	entries, err := os.ReadDir(settings)
	if err != nil {
		t.Fatalf("listing the settings directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the settings directory holds %v after the failed read, want no files", entries)
	}
}
