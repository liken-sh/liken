package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMergedSBOMValidates(t *testing.T) {
	if err := validate(marshal(t, fixtureBOM(t))); err != nil {
		t.Fatal(err)
	}
}

func TestSpecVersionComesFromTheSchema(t *testing.T) {
	if specVersion != "1.7" {
		t.Errorf("specVersion is %q, and schema/ holds bom-1.7.schema.json", specVersion)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := []struct{ name, document string }{
		{"no bomFormat", `{"specVersion": "1.7"}`},
		{"another format", `{"bomFormat": "SPDX", "specVersion": "1.7"}`},
		{"an unknown field", `{"bomFormat": "CycloneDX", "specVersion": "1.7", "colour": "blue"}`},
		{"a component with no name", `{"bomFormat": "CycloneDX", "specVersion": "1.7", "components": [{"type": "library"}]}`},
		{"a license outside SPDX", `{"bomFormat": "CycloneDX", "specVersion": "1.7", "components": [{"type": "library", "name": "x", "licenses": [{"license": {"id": "Not-A-License"}}]}]}`},
		{"not JSON", `{`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validate([]byte(c.document)); err == nil {
				t.Error("validate accepted the document")
			}
		})
	}
}

func TestValidateAcceptsTheSmallestBOM(t *testing.T) {
	if err := validate([]byte(`{"bomFormat": "CycloneDX", "specVersion": "1.7"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestValidationErrorNamesTheField(t *testing.T) {
	err := validate([]byte(`{"bomFormat": "CycloneDX", "specVersion": "1.7", "colour": "blue"}`))
	if err == nil || !strings.Contains(err.Error(), "colour") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

// The schema files are the bytes that their release tag serves, and
// schema/latest.sh --bump records the sha256 of each one in
// schema/SHA256SUMS. A change to a file that no bump wrote, such as a
// newline that an editor or a fixer adds, fails here.
func TestSchemaFilesMatchTheirDigests(t *testing.T) {
	sums, err := os.ReadFile("schema/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := schemaFiles.ReadDir("schema")
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		digest, name, _ := strings.Cut(line, "  ")
		pinned[name] = digest
	}
	if len(pinned) != len(entries) {
		t.Errorf("SHA256SUMS names %d files, and schema/ holds %d", len(pinned), len(entries))
	}
	for _, e := range entries {
		data, err := schemaFiles.ReadFile("schema/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != pinned[e.Name()] {
			t.Errorf("schema/%s has the sha256 %s, and SHA256SUMS pins %q", e.Name(), got, pinned[e.Name()])
		}
	}
}
