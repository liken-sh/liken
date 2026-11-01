package manifest

import (
	"encoding/json"
	"testing"
)

// componentsByName reads the fixture manifest and indexes its
// components by name, through JSON, the form that both SBOMs write.
func componentsByName(t *testing.T) map[string]map[string]any {
	t.Helper()
	components, err := Components("testdata/pin.toml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(components)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, c := range decoded {
		byName[c["name"].(string)] = c
	}
	return byName
}

func TestComponentsGivesOneComponentForEachEntry(t *testing.T) {
	components, err := Components("testdata/pin.toml")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range components {
		names = append(names, c["name"].(string))
	}
	want := []string{"grub2-common", "k3s", "linux-modules", "weston"}
	if len(names) != len(want) {
		t.Fatalf("got the components %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("got the components %v, want %v in order", names, want)
		}
	}
}

func TestComponentsCopiesEachField(t *testing.T) {
	cases := []struct {
		name, field, want string
	}{
		{"k3s", "bom-ref", "pin:k3s"},
		{"k3s", "version", "v1.37.1+k3s1"},
		{"k3s", "purl", "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"},
		{"k3s", "scope", "required"},
		{"k3s", "type", "file"},
		{"grub2-common", "scope", "excluded"},
		{"weston", "type", "application"},
		{"weston", "cpe", "cpe:2.3:a:wayland:weston:16.0.2:*:*:*:*:*:*:*"},
	}
	byName := componentsByName(t)
	for _, c := range cases {
		t.Run(c.name+" "+c.field, func(t *testing.T) {
			if got := byName[c.name][c.field]; got != c.want {
				t.Errorf("%s of %s is %v, want %s", c.field, c.name, got, c.want)
			}
		})
	}
}

func TestComponentsLeavesOutAnAbsentCPE(t *testing.T) {
	if cpe, ok := componentsByName(t)["k3s"]["cpe"]; ok {
		t.Errorf("k3s has the cpe %v, and its entry names none", cpe)
	}
}

func TestComponentsGivesTheDigestTheLicenseAndTheDownload(t *testing.T) {
	k3s := componentsByName(t)["k3s"]
	data, err := json.Marshal(map[string]any{
		"hashes":             k3s["hashes"],
		"licenses":           k3s["licenses"],
		"externalReferences": k3s["externalReferences"],
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"externalReferences":[{"hashes":[{"alg":"SHA-256","content":"92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"}],"type":"distribution","url":"https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s"}],"hashes":[{"alg":"SHA-256","content":"92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"}],"licenses":[{"expression":"Apache-2.0"}]}`
	if string(data) != want {
		t.Errorf("got %s\nwant %s", data, want)
	}
}

func TestComponentsFailsOnAMissingManifest(t *testing.T) {
	if _, err := Components("testdata/none.toml"); err == nil {
		t.Error("Components read a manifest that does not exist")
	}
}
