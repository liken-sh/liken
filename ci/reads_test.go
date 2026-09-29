package main

import (
	"reflect"
	"testing"
)

// readsFixture is a repository with an operator that has a Go job, a
// manual, a Rust crate in ui/, and a prek job, and the four components
// it depends on: kubernetes, a Go module that its go.mod replaces;
// screen, a crate that its crate takes by path; brand, the theme and a
// crate of its own; and base, a pinned image that its image builds on.
// The pinned base is published.
func readsFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t, map[string]string{
		"brand/package.toml":      "[package]\nname = \"brand\"\n",
		"brand/liken.css":         "body {}\n",
		"brand/layouts/page.html": "<html>\n",
		"brand/iced/Cargo.toml":   "[package]\nname = \"liken-iced\"\n",
		"brand/iced/src/lib.rs":   "pub const CSS: &str = include_str!(\"../../liken.css\");\n",
		"kubernetes/package.toml": "[package]\nname = \"kubernetes\"\n",
		"kubernetes/go.mod":       "module example.com/kubernetes\n",
		"kubernetes/watch.go":     "package kubernetes\n",
		"screen/package.toml":     "[package]\nname = \"screen\"\n",
		"screen/Cargo.toml":       "[package]\nname = \"screen\"\n[lib]\npath = \"src/lib.rs\"\n",
		"screen/src/lib.rs":       "pub fn draw() {}\n",
		"base/package.toml":       "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":         "FROM scratch\n",
		"operator/package.toml": `[package]
name = "operator"
[depends]
components = ["base", "brand", "kubernetes", "screen"]
[docs]
prefix = "operator"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test-go"
[[jobs]]
name = "docs"
toolchain = "hugo"
module = "docs"
run = "make test-docs"
[[jobs]]
name = "rust"
toolchain = "rust"
dir = "ui"
run = "make test"
[[jobs]]
name = "prek"
toolchain = "prek"
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "operator"
`,
		"operator/go.mod":                 "module example.com/operator\n\nreplace example.com/kubernetes => ../kubernetes\n",
		"operator/main.go":                "package main\n",
		"operator/Dockerfile":             "FROM base\nCOPY main.go /\n",
		"operator/.dockerignore":          "*\n!main.go\n",
		"operator/deploy/crd.yaml":        "kind: CustomResourceDefinition\n",
		"operator/docs/go.mod":            "module example.com/docs\n\nreplace (\n\texample.com/brand => ../../brand\n)\n",
		"operator/docs/index.md":          "a manual\n",
		"operator/docs/manual_test.go":    "package docs\n",
		"operator/skills/a/SKILL.md":      "a skill\n",
		"operator/smoke/operator.sh":      "true\n",
		"operator/ui/Cargo.toml":          "[package]\nname = \"ui\"\n[lib]\npath = \"src/lib.rs\"\n[dependencies]\nscreen = { path = \"../../screen\" }\n[dev-dependencies.liken-iced]\npath = \"../../brand/iced\"\n",
		"operator/ui/src/lib.rs":          "pub fn ui() {}\n",
		"operator/ui/cases.json":          "[]\n",
		"operator/ui/rust-toolchain.toml": "[toolchain]\n",
	})
	return r, r.run("rev-parse", "HEAD")
}

// Each job reads the files its toolchain reads: a Go job reads no Rust
// source and no page of the manual, a Rust job reads no Go source and
// no page of the manual, and a manual reads no Rust source. A job reads
// a dependency only through what its toolchain builds from: a Go
// module that its go.mod replaces, a crate that its crate takes by path,
// or brand's theme for a manual. A pinned dependency is an image, and
// only images read it.
func TestAChangeRunsTheJobsWhoseToolchainReadsIt(t *testing.T) {
	cases := []struct {
		name string
		file string
		jobs []string
	}{
		{"its Go source runs every job but the Rust one", "operator/main.go", []string{"go", "docs", "prek"}},
		{"its Rust source runs the Rust job", "operator/ui/src/lib.rs", []string{"rust", "prek"}},
		{"its crate's manifest runs the Rust job", "operator/ui/Cargo.toml", []string{"rust", "prek"}},
		{"its crate's toolchain pin runs the Rust job", "operator/ui/rust-toolchain.toml", []string{"rust", "prek"}},
		{"a data file in the crate runs every job", "operator/ui/cases.json", []string{"go", "docs", "rust", "prek"}},
		{"its manifests run every job", "operator/deploy/crd.yaml", []string{"go", "docs", "rust", "prek"}},
		{"a page of its manual runs the manual", "operator/docs/index.md", []string{"docs", "prek"}},
		{"a Go file of its manual runs the Go job too, whose gofmt reads it", "operator/docs/manual_test.go", []string{"go", "docs", "prek"}},
		{"a generated skill runs the manual", "operator/skills/a/SKILL.md", []string{"docs", "prek"}},
		{"a smoke check runs the manual", "operator/smoke/operator.sh", []string{"docs", "prek"}},
		{"a Go module it replaces runs its Go job and its manual", "kubernetes/watch.go", []string{"go", "docs", "prek"}},
		{"a crate it takes by path runs its Rust job", "screen/src/lib.rs", []string{"rust", "prek"}},
		{"brand's stylesheet runs the crate that reads it and the manual", "brand/liken.css", []string{"docs", "rust", "prek"}},
		{"brand's crate runs the crate that takes it", "brand/iced/src/lib.rs", []string{"rust", "prek"}},
		{"brand's theme runs the manual, and the crate beside brand's", "brand/layouts/page.html", []string{"docs", "rust", "prek"}},
		{"a pinned base runs no job", "base/Dockerfile", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := readsFixture(t)
			r.write(c.file, "changed\n")
			r.commit("the change")
			decisions, err := r.pinnedPlanner(map[string]bool{"base": true}, nil).Plan(Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})
			if err != nil {
				t.Fatal(err)
			}
			if d := decisions["operator"]; !reflect.DeepEqual(d.Jobs, c.jobs) {
				t.Errorf("operator: jobs %v, want %v", d.Jobs, c.jobs)
			}
		})
	}
}

// A crate manifest that does not parse names no dependency the plan can
// trust, so the Rust job reads every dependency, and it runs and fails
// on the manifest.
func TestAManifestThatDoesNotParseReadsEveryDependency(t *testing.T) {
	r, _ := readsFixture(t)
	r.write("operator/ui/Cargo.toml", "[package\n")
	before := r.commit("break the manifest")
	r.write("kubernetes/crd.yaml", "kind: CustomResourceDefinition\n")
	r.commit("the change")
	decisions, err := r.pinnedPlanner(map[string]bool{"base": true}, nil).Plan(Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; !reflect.DeepEqual(d.Jobs, []string{"go", "docs", "rust", "prek"}) {
		t.Errorf("operator: jobs %v", d.Jobs)
	}
}

// osFixture is an OS component with a manual, the OS build, and the
// manual's job.
func osFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t, map[string]string{
		"liken/package.toml":      "[package]\nname = \"liken\"\n[docs]\nprefix = \"\"\n[[jobs]]\nname = \"docs\"\ntoolchain = \"hugo\"\nmodule = \"docs\"\nrun = \"make test-docs\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
		"liken/init/main.go":      "package main\n",
		"liken/docs/index.md":     "a manual\n",
		"liken/skills/a/SKILL.md": "a skill\n",
	})
	return r, r.run("rev-parse", "HEAD")
}

// The OS build builds every file of the OS but the manual, which
// ships on its own path to the site.
func TestTheOSBuildReadsEveryFileButTheManual(t *testing.T) {
	cases := []struct {
		name string
		file string
		jobs []string
	}{
		{"its source runs the build and the manual", "liken/init/main.go", []string{"docs", "build"}},
		{"a page of its manual runs the manual alone", "liken/docs/index.md", []string{"docs"}},
		{"a generated skill runs the manual alone", "liken/skills/a/SKILL.md", []string{"docs"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := osFixture(t)
			r.write(c.file, "changed\n")
			r.commit("the change")
			if d := planGate(t, r, nil, Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})["liken"]; !reflect.DeepEqual(d.Jobs, c.jobs) {
				t.Errorf("liken: jobs %v, want %v", d.Jobs, c.jobs)
			}
		})
	}
}
