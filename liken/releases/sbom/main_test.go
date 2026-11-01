package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixtureArgs is the command line that writes the SBOM of the
// fixtures into a directory.
func fixtureArgs(out string) []string {
	return []string{
		"-version", "2026.11.01-901",
		"-out", out,
		"-buildroot", "testdata/buildroot.cdx.json",
		"-syft", "liken=testdata/syft-root.cdx.json",
		"-syft", "pin:k3s=testdata/syft-k3s.cdx.json",
		"-claim", "/bin/k3s=pin:k3s",
		"-claim", "/lib/modules/=pin:linux-modules",
		"testdata/pin.toml",
	}
}

func TestRunWritesAValidSBOM(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sbom.cdx.json")
	if err := run(fixtureArgs(out)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(data); err != nil {
		t.Fatal(err)
	}
	var written struct {
		SpecVersion string `json:"specVersion"`
		Components  []any  `json:"components"`
	}
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	if written.SpecVersion != "1.7" || len(written.Components) != 13 {
		t.Errorf("wrote specVersion %s with %d components", written.SpecVersion, len(written.Components))
	}
}

func TestRunWritesTheSameBytesForTheSameInputs(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
	for _, out := range []string{first, second} {
		if err := run(fixtureArgs(out)); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Error("two runs on the same inputs wrote different SBOMs")
	}
}

func TestRunRefuses(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sbom.cdx.json")
	cases := []struct {
		name string
		args []string
	}{
		{"no version", []string{"-out", out, "testdata/pin.toml"}},
		{"no output", []string{"-version", "2026.11.01-901", "testdata/pin.toml"}},
		{"no manifest", []string{"-version", "2026.11.01-901", "-out", out}},
		{"a scan with no parent", append(fixtureArgs(out), "-syft", "testdata/syft-k3s.cdx.json")},
		{"a claim with no component", append(fixtureArgs(out), "-claim", "/bin/k3s")},
		{"an unknown flag", append(fixtureArgs(out), "-colour", "blue")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := run(c.args); err == nil {
				t.Error("run accepted the command line")
			}
		})
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused run wrote the SBOM")
	}
}
