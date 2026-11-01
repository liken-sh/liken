package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// fixtureInputs names the fixtures of testdata/: one manifest, one
// buildroot SBOM, a syft scan of the root filesystem, and a syft scan
// of the programs that k3s carries inside its binary.
func fixtureInputs() inputs {
	return inputs{
		version:   "2026.11.01-901",
		manifests: []string{"testdata/pin.toml"},
		buildroot: "testdata/buildroot.cdx.json",
		scans: []scan{
			{parent: rootRef, file: "testdata/syft-root.cdx.json"},
			{parent: "pin:k3s", file: "testdata/syft-k3s.cdx.json"},
		},
		claims: map[string]string{
			"/bin/k3s":      "pin:k3s",
			"/sbin/mke2fs":  "buildroot:e2fsprogs",
			"/lib/modules/": "pin:linux-modules",
		},
	}
}

func fixtureBOM(t *testing.T) *bom {
	t.Helper()
	b, err := merge(fixtureInputs())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func component(t *testing.T, b *bom, ref string) map[string]any {
	t.Helper()
	for _, c := range b.Components {
		if c["bom-ref"] == ref {
			return c
		}
	}
	t.Fatalf("the SBOM has no component %s", ref)
	return nil
}

func dependsOn(b *bom, ref string) []string {
	for _, d := range b.Dependencies {
		if d.Ref == ref {
			return d.DependsOn
		}
	}
	return nil
}

func TestManifestEntryBecomesComponent(t *testing.T) {
	b := fixtureBOM(t)
	k3s := component(t, b, "pin:k3s")
	grub := component(t, b, "pin:grub2-common")
	cases := []struct {
		name      string
		got, want any
	}{
		{"name", k3s["name"], "k3s"},
		{"version", k3s["version"], "v1.37.1+k3s1"},
		{"purl", k3s["purl"], "pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1"},
		{"type", k3s["type"], "file"},
		{"scope", k3s["scope"], "required"},
		{"hash", k3s["hashes"], []hash{{Alg: "SHA-256", Content: "92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"}}},
		{"license", k3s["licenses"], []license{{Expression: "Apache-2.0"}}},
		{"download", k3s["externalReferences"], []reference{{
			Type:   "distribution",
			URL:    "https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s",
			Hashes: []hash{{Alg: "SHA-256", Content: "92b94582eb7b34a8cf532e6006842b9ed215a6783cf56a3dd6271fd35243b2c0"}},
		}}},
		{"excluded scope", grub["scope"], "excluded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !equalJSON(t, c.got, c.want) {
				t.Errorf("got %v, want %v", c.got, c.want)
			}
		})
	}
}

func TestSyftProgramThatAManifestNamesIsTheManifestComponent(t *testing.T) {
	b := fixtureBOM(t)
	for _, c := range b.Components {
		if c["purl"] == "pkg:golang/github.com/k3s-io/k3s@v1.37.1" {
			t.Errorf("the SBOM keeps syft's component of /bin/k3s: %v", c["bom-ref"])
		}
	}
	deps := dependsOn(b, "pin:k3s")
	for _, want := range []string{
		"syft:syft-root:pkg:golang/golang.org/x/sync@v0.22.0?package-id=f12520e48c54dcb7",
		"syft:syft-root:pkg:golang/stdlib@1.26.8?package-id=dff74e7ab8d6bee5",
		"syft:syft-k3s:pkg:golang/github.com/k3s-io/containerd/v2@v2.3.4-k3s1?package-id=0a1b2c3d4e5f6071",
	} {
		if !slices.Contains(deps, want) {
			t.Errorf("pin:k3s depends on %v, which lacks %s", deps, want)
		}
	}
}

func TestReleaseDependsOnEachSource(t *testing.T) {
	b := fixtureBOM(t)
	want := []string{
		"buildroot:buildroot",
		"pin:grub2-common",
		"pin:k3s",
		"pin:linux-modules",
		"syft:syft-root:pkg:golang/github.com/liken-sh/liken/liken@v0.0.0-20261101170642-9fd142ff982f?package-id=b2a89c489ac4a1d0",
	}
	if got := dependsOn(b, rootRef); !slices.Equal(got, want) {
		t.Errorf("the release depends on %v, want %v", got, want)
	}
}

func TestBuildrootComponentsKeepTheirGraph(t *testing.T) {
	b := fixtureBOM(t)
	want := []string{"buildroot:host-autoconf", "buildroot:util-linux"}
	if got := dependsOn(b, "buildroot:e2fsprogs"); !slices.Equal(got, want) {
		t.Errorf("e2fsprogs depends on %v, want %v", got, want)
	}
	if got := component(t, b, "buildroot:host-autoconf")["scope"]; got != "excluded" {
		t.Errorf("a host package has the scope %v, want excluded", got)
	}
	if got := component(t, b, "buildroot:buildroot")["version"]; got != "2026.11-git" {
		t.Errorf("buildroot has the version %v", got)
	}
}

func TestEachComponentNamesItsSource(t *testing.T) {
	b := fixtureBOM(t)
	got := map[string]int{}
	for _, c := range b.Components {
		got[property(c, sourceProperty)]++
	}
	want := map[string]int{"manifest": 3, "buildroot": 4, "syft": 6}
	if !equalJSON(t, got, want) {
		t.Errorf("components by source: %v, want %v", got, want)
	}
}

func TestReleaseIsTheOneOperatingSystem(t *testing.T) {
	b := fixtureBOM(t)
	root := b.Metadata.Component
	if root["name"] != "liken" || root["version"] != "2026.11.01-901" || root["type"] != "operating-system" {
		t.Errorf("the SBOM describes %v %v %v", root["type"], root["name"], root["version"])
	}
	for _, c := range b.Components {
		if c["type"] == "operating-system" || c["type"] == "file" && property(c, sourceProperty) == "syft" {
			t.Errorf("the SBOM keeps syft's %v component %v", c["type"], c["bom-ref"])
		}
	}
}

func TestCompositionsStateCompletenessWhereItIsKnown(t *testing.T) {
	b := fixtureBOM(t)
	aggregate := map[string]string{}
	for _, c := range b.Compositions {
		for _, ref := range c.Dependencies {
			aggregate[ref] = c.Aggregate
		}
	}
	cases := []struct{ ref, want string }{
		{rootRef, "incomplete"},
		{"pin:k3s", "incomplete"},
		{"buildroot:buildroot", "complete"},
		{"syft:syft-root:pkg:golang/github.com/liken-sh/liken/liken@v0.0.0-20261101170642-9fd142ff982f?package-id=b2a89c489ac4a1d0", "complete"},
		{"syft:syft-k3s:pkg:golang/github.com/k3s-io/containerd/v2@v2.3.4-k3s1?package-id=0a1b2c3d4e5f6071", ""},
		{"pin:grub2-common", ""},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			if got := aggregate[c.ref]; got != c.want {
				t.Errorf("aggregate %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildrootVulnerabilitiesAreLeftOut(t *testing.T) {
	data := marshal(t, fixtureBOM(t))
	if containsKey(t, data, "vulnerabilities") {
		t.Error("the SBOM carries buildroot's vulnerabilities")
	}
}

func TestEveryReferenceNamesAComponent(t *testing.T) {
	b := fixtureBOM(t)
	refs := map[string]bool{rootRef: true}
	for _, c := range b.Components {
		refs[c["bom-ref"].(string)] = true
	}
	for _, d := range b.Dependencies {
		for _, ref := range append([]string{d.Ref}, d.DependsOn...) {
			if !refs[ref] {
				t.Errorf("a dependency names %s, which no component has", ref)
			}
		}
	}
}

func TestMergeRefuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(*inputs)
	}{
		{"a claim of no component", func(in *inputs) { in.claims["/bin/k3s"] = "pin:nothing" }},
		{"a scan inside no component", func(in *inputs) { in.scans[1].parent = "pin:nothing" }},
		{"two scans of one name", func(in *inputs) { in.scans[1].file = in.scans[0].file }},
		{"a missing manifest", func(in *inputs) { in.manifests = []string{"testdata/none.toml"} }},
		{"a missing buildroot SBOM", func(in *inputs) { in.buildroot = "testdata/none.cdx.json" }},
		{"a version outside the grammar", func(in *inputs) { in.version = "2026.11.1" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixtureInputs()
			c.change(&in)
			if _, err := merge(in); err == nil {
				t.Error("merge accepted the inputs")
			}
		})
	}
}

// equalJSON compares two values by their JSON form, so a typed value
// that a test writes compares with the decoded form that a component
// holds.
func equalJSON(t *testing.T, got, want any) bool {
	t.Helper()
	return string(marshal(t, got)) == string(marshal(t, want))
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	_, ok := fields[key]
	return ok
}

// A claim that ends in a slash names a directory, and claims each file
// under it: the kernel modules that syft lists one by one are the
// files of one manifest entry.
func TestClaimOfADirectoryTakesEachFileUnderIt(t *testing.T) {
	b := fixtureBOM(t)
	for _, c := range b.Components {
		if property(c, "syft:package:type") == "linux-kernel-module" {
			t.Errorf("the SBOM keeps syft's kernel module %v", c["bom-ref"])
		}
	}
}
