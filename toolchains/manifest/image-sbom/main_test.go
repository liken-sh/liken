package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// written is the part of the record that the tests read.
type written struct {
	BOMFormat    string           `json:"bomFormat"`
	SpecVersion  string           `json:"specVersion"`
	SerialNumber string           `json:"serialNumber"`
	Components   []map[string]any `json:"components"`
	Dependencies []dependency     `json:"dependencies"`
	Vulns        []any            `json:"vulnerabilities"`
}

// write runs the command with the arguments and reads the record that
// it wrote.
func write(t *testing.T, args ...string) written {
	t.Helper()
	out := filepath.Join(t.TempDir(), "image.cdx.json")
	if err := run(append([]string{"-out", out}, args...)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var w written
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func refs(w written) []string {
	var refs []string
	for _, c := range w.Components {
		refs = append(refs, c["bom-ref"].(string))
	}
	return refs
}

func TestRunWritesACycloneDXDocument(t *testing.T) {
	w := write(t, "testdata/pin.toml")
	if w.BOMFormat != "CycloneDX" || w.SpecVersion != "1.6" || !strings.HasPrefix(w.SerialNumber, "urn:uuid:") {
		t.Errorf("wrote bomFormat %q, specVersion %q, serialNumber %q", w.BOMFormat, w.SpecVersion, w.SerialNumber)
	}
}

// An entry with the scope excluded is a file that only the build
// reads, and syft ignores the scope, so the record leaves it out.
func TestRunLeavesOutWhatOnlyTheBuildReads(t *testing.T) {
	got := refs(write(t, "testdata/pin.toml"))
	if !slices.Equal(got, []string{"pin:weston"}) {
		t.Errorf("wrote the components %v, want only pin:weston", got)
	}
}

func TestRunKeepsTheCPEAndThePurl(t *testing.T) {
	weston := write(t, "testdata/pin.toml").Components[0]
	if weston["cpe"] != "cpe:2.3:a:wayland:weston:16.0.2:*:*:*:*:*:*:*" || !strings.HasPrefix(weston["purl"].(string), "pkg:generic/weston@16.0.2?") {
		t.Errorf("wrote weston with the cpe %v and the purl %v", weston["cpe"], weston["purl"])
	}
}

// buildroot's SBOM lists the host tools that ran during the build, with
// the scope excluded, and virtual packages with no version. The image
// holds neither, so the record keeps the target packages alone.
func TestRunKeepsBuildrootsTargetPackages(t *testing.T) {
	got := refs(write(t, "-buildroot", "testdata/buildroot.cdx.json"))
	want := []string{"buildroot:bluez5_utils-static", "buildroot:dbus"}
	if !slices.Equal(got, want) {
		t.Errorf("wrote the components %v, want %v", got, want)
	}
}

func TestRunKeepsTheGraphBetweenKeptPackages(t *testing.T) {
	w := write(t, "-buildroot", "testdata/buildroot.cdx.json")
	want := []dependency{
		{Ref: "buildroot:bluez5_utils-static", DependsOn: []string{"buildroot:dbus"}},
		{Ref: "buildroot:dbus"},
	}
	if len(w.Dependencies) != len(want) {
		t.Fatalf("wrote the graph %+v, want %+v", w.Dependencies, want)
	}
	for i := range want {
		if w.Dependencies[i].Ref != want[i].Ref || !slices.Equal(w.Dependencies[i].DependsOn, want[i].DependsOn) {
			t.Errorf("wrote the graph %+v, want %+v", w.Dependencies, want)
		}
	}
}

// buildroot's vulnerabilities are the result of its check on the day
// of the build, and a scanner that reads the image matches the
// advisories of the day it runs.
func TestRunLeavesOutBuildrootsVulnerabilities(t *testing.T) {
	if w := write(t, "-buildroot", "testdata/buildroot.cdx.json"); w.Vulns != nil {
		t.Errorf("wrote the vulnerabilities %v", w.Vulns)
	}
}

func TestRunWritesTheSameBytesForTheSameInputs(t *testing.T) {
	dir := t.TempDir()
	var outputs []string
	for _, name := range []string{"first.json", "second.json"} {
		out := filepath.Join(dir, name)
		if err := run([]string{"-out", out, "-buildroot", "testdata/buildroot.cdx.json", "testdata/pin.toml"}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(data))
	}
	if outputs[0] != outputs[1] {
		t.Error("two runs on the same inputs wrote different records")
	}
}

func TestRunRefuses(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no output", []string{"testdata/pin.toml"}, "-out"},
		{"no input", []string{"-out", "x.json"}, "name a pin.toml"},
		{"an entry whose type syft drops", []string{"-out", "x.json", "testdata/untyped.toml"}, "[cacert] has the type file"},
		{"a missing manifest", []string{"-out", "x.json", "testdata/none.toml"}, "none.toml"},
		{"a missing buildroot SBOM", []string{"-out", "x.json", "-buildroot", "testdata/none.json"}, "none.json"},
	}
	testdata := testdataDir(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			args := slices.Clone(c.args)
			for i, a := range args {
				if strings.HasPrefix(a, "testdata/") {
					args[i] = filepath.Join(testdata, strings.TrimPrefix(a, "testdata/"))
				}
			}
			err := run(args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("run(%v) returned %v, want an error that names %q", c.args, err, c.want)
			}
		})
	}
}

// testdataDir is the absolute path of testdata/, because a refusal runs
// in a temporary directory so that it writes no file into the tree.
func testdataDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
