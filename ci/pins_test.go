package main

import (
	"strings"
	"testing"
)

// pinsTree is a repository in which every shared pin names one
// version: two Go modules, two Cargo workspaces, two hook files, and
// a manifest and a guide that name images of images.toml.
func pinsTree(t *testing.T, changes map[string]string) string {
	t.Helper()
	tree := map[string]string{
		"toolchains/images.toml": "[build]\n" +
			"golang = \"golang:1.27.3-bookworm@sha256:aaa\"\n" +
			"rust = \"rust:1.98.1-bookworm@sha256:bbb\"\n" +
			"debian = \"debian:sid-slim@sha256:ccc\"\n" +
			"[run]\n" +
			"curl = \"curlimages/curl:8.22.0\"\n" +
			"mosquitto = \"eclipse-mosquitto:2.0.22\"\n",
		"a/go.mod": "module a\n\ngo 1.27.3\n\nrequire (\n\tgithub.com/x/y v1.2.0\n\tgolang.org/x/sys v0.48.0 // indirect\n\tgithub.com/liken-sh/liken/kubernetes v0.0.0\n)\n\n" +
			"replace github.com/liken-sh/liken/kubernetes => ../kubernetes\n\ntool github.com/t/cover/v2\n\nrequire github.com/t/cover/v2 v2.19.0 // indirect\n",
		"b/go.mod":              "module b\n\ngo 1.27.3\n\nrequire github.com/x/y v1.2.0\nrequire golang.org/x/sys v0.47.0 // indirect\nrequire github.com/liken-sh/liken/kubernetes v0.0.0-20260101000000-abcdef\nreplace github.com/liken-sh/liken/kubernetes => ../kubernetes\nrequire github.com/t/cover/v2 v2.19.0 // indirect\ntool github.com/t/cover/v2\n",
		"a/rust-toolchain.toml": "[toolchain]\nchannel = \"1.98.1\"\n",
		"a/Cargo.toml": "[package]\nname = \"a\"\n[dependencies]\nserde = \"1.0.200\"\ntokio = { version = \"1.40\", features = [\"full\"] }\nlocal = { path = \"../local\" }\n" +
			"[dev-dependencies]\ntempfile = \"3.10\"\n[target.'cfg(unix)'.dependencies]\nlibc = \"0.2.150\"\n",
		"b/Cargo.toml":              "[workspace]\nmembers = [\"c\"]\n[workspace.dependencies]\nserde = { version = \"1.0.200\" }\ntokio = \"1.40\"\n",
		"b/c/Cargo.toml":            "[package]\nname = \"c\"\n[dependencies]\nserde = { workspace = true }\n[build-dependencies]\nlibc = \"0.2.150\"\n",
		"a/.pre-commit-config.yaml": "repos:\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v6.0.0\n    hooks:\n      - id: trailing-whitespace\n  - repo: local\n    hooks:\n      - id: gofmt\n",
		"b/.pre-commit-config.yaml": "repos:\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v6.0.0\n    hooks:\n      - id: end-of-file-fixer\n",
		"a/deploy/bus.yaml":         "spec:\n  containers:\n    - image: eclipse-mosquitto:2.0.22\n    - image: docker.io/curlimages/curl:8.22.0\n",
		"a/docs/guide.md":           "Run `kubectl run probe --image=curlimages/curl:8.22.0`, and build from\n`FROM debian:sid-slim`, or from `golang:1.27.3-bookworm`.\n",
		"a/Makefile":                "probe:\n\tdocker run --rm docker.io/library/debian:sid-slim true\n",
		"a/probe_test.go":           "package a\n\nconst image = \"curlimages/curl:8.22.0\"\n",
		"a/src/lib.rs":              "const IMAGE: &str = \"eclipse-mosquitto:2.0.22\";\n",
		// Out of scope: our own images, flux's generated manifests,
		// test data, a cache, the plans, and the fixtures of ci's own
		// tests.
		"a/deploy/app.yaml":                  "image: ghcr.io/liken-sh/curl:1.0.0\n",
		"a/flux/gotk-components.yaml":        "image: curlimages/curl:7.0.0\n",
		"a/testdata/go.mod":                  "module t\n\ngo 1.20\n",
		"a/testdata/old.yaml":                "image: curlimages/curl:7.0.0\n",
		"a/.cache/go.mod":                    "module c\n\ngo 1.20\n",
		"a/.gitignore":                       "/download/\n",
		"a/download/.pre-commit-config.yaml": "repos:\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v4.0.1\n",
		"a/NOTICES.md":                       "mke2fs builds on `debian:bullseye`.\n",
		"a/plans/1-history.md":               "We ran `debian:12-slim` then.\n",
		"ci/dockerfile_test.go":              "package main\n\nconst f = \"FROM debian:trixie-slim\"\n",
		"a/docs/variables.md":                "Write `debian:${VERSION}` or `golang:<tag>`.\n",
		"toolchains/.pre-commit-config.yaml": "repos:\n  - repo: meta\n    hooks:\n      - id: check-hooks-apply\n",
		"a/layout/liken-thing-v1.xml":        "<protocol name=\"liken_thing_v1\"/>\n",
		"b/src/liken-thing-v1.xml":           "<protocol name=\"liken_thing_v1\"/>\n",
		"b/src/other-v1.xml":                 "<protocol name=\"other_v1\"/>\n",
	}
	for name, text := range changes {
		tree[name] = text
	}
	root := writeTree(t, tree)
	if _, err := (Git{Dir: root}).run("init", "-q"); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEverySharedPinNamesOneVersion(t *testing.T) {
	findings, err := checkPins(pinsTree(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the tree drifts:\n%s", strings.Join(findings, "\n"))
	}
}

func TestADriftedPinIsFound(t *testing.T) {
	cases := map[string]struct {
		file, text, wants string
	}{
		"a go directive": {"b/go.mod", "module b\n\ngo 1.27.2\n",
			"b/go.mod names go 1.27.2, and the golang image in toolchains/images.toml is 1.27.3"},
		"a Rust toolchain": {"b/rust-toolchain.toml", "[toolchain]\nchannel = \"1.97.0\"\n",
			"b/rust-toolchain.toml names Rust 1.97.0, and the rust image in toolchains/images.toml is 1.98.1"},
		"a Go module": {"b/go.mod", "module b\n\ngo 1.27.3\n\nrequire github.com/x/y v1.3.0\n",
			"the Go module github.com/x/y has 2 versions: v1.2.0 in a/go.mod; v1.3.0 in b/go.mod"},
		"a Go tool": {"b/go.mod", "module b\n\ngo 1.27.3\n\nrequire github.com/t/cover/v2 v2.18.0 // indirect\ntool github.com/t/cover/v2\n",
			"the Go module github.com/t/cover/v2 has 2 versions: v2.18.0 in b/go.mod; v2.19.0 in a/go.mod"},
		"a crate": {"b/Cargo.toml", "[workspace]\n[workspace.dependencies]\nserde = { version = \"1.0.201\" }\n",
			"the crate serde has 2 versions: 1.0.200 in a/Cargo.toml; 1.0.201 in b/Cargo.toml"},
		"a build crate": {"b/c/Cargo.toml", "[package]\nname = \"c\"\n[build-dependencies]\nlibc = \"0.2.151\"\n",
			"the crate libc has 2 versions: 0.2.150 in a/Cargo.toml; 0.2.151 in b/c/Cargo.toml"},
		"a hook": {"b/.pre-commit-config.yaml", "repos:\n  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v5.0.0\n    hooks:\n      - id: end-of-file-fixer\n",
			"the hook repository https://github.com/pre-commit/pre-commit-hooks has 2 versions: v5.0.0 in b/.pre-commit-config.yaml; v6.0.0 in a/.pre-commit-config.yaml"},
		"a run image in a manifest": {"a/deploy/bus.yaml", "image: eclipse-mosquitto:2\n",
			"a/deploy/bus.yaml:1 names eclipse-mosquitto:2, and toolchains/images.toml names eclipse-mosquitto:2.0.22"},
		"a build image in a guide": {"a/docs/guide.md", "Start from `debian:12-slim`.\n",
			"a/docs/guide.md:1 names debian:12-slim, and toolchains/images.toml names debian:sid-slim"},
		"a Docker Hub image by its full name": {"a/Makefile", "probe:\n\tdocker run docker.io/library/debian:trixie-slim\n",
			"a/Makefile:2 names docker.io/library/debian:trixie-slim, and toolchains/images.toml names debian:sid-slim"},
		"an image in a skill": {"a/skills/probe/SKILL.md", "kubectl run --image=curlimages/curl:8.5.0.\n",
			"a/skills/probe/SKILL.md:1 names curlimages/curl:8.5.0, and toolchains/images.toml names curlimages/curl:8.22.0"},
		"an image in a Go test": {"a/probe_test.go", "package a\n\nconst image = \"curlimages/curl:8.5.0\"\n",
			"a/probe_test.go:3 names curlimages/curl:8.5.0"},
		"an image in a Rust file": {"a/src/lib.rs", "const IMAGE: &str = \"eclipse-mosquitto:2\";\n",
			"a/src/lib.rs:1 names eclipse-mosquitto:2,"},
		"a copy of a protocol": {"b/src/liken-thing-v1.xml", "<protocol name=\"liken_thing_v1\" version=\"2\"/>\n",
			"the Wayland protocol liken-thing-v1.xml has 2 versions:"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkPins(pinsTree(t, map[string]string{c.file: c.text}))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("got:\n%s\nwant a finding with %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}

func TestThePinsCommandFailsOnDrift(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		fails   bool
	}{
		"a tree with one version of each pin": {nil, false},
		"a tree with a drifted pin":           {map[string]string{"b/go.mod": "module b\n\ngo 1.26.0\n"}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := pinsTree(t, c.changes)
			w := newWorld(t, &fakeRegistry{})
			if err := w.run([]string{"pins", "-root", root}); (err != nil) != c.fails {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestAPinFileThatDoesNotParseIsAnError(t *testing.T) {
	for _, file := range []string{"a/Cargo.toml", "a/rust-toolchain.toml", "a/.pre-commit-config.yaml", "toolchains/images.toml"} {
		t.Run(file, func(t *testing.T) {
			if _, err := checkPins(pinsTree(t, map[string]string{file: "[[[\n  - :\n"})); err == nil || !strings.Contains(err.Error(), file) {
				t.Errorf("got %v, want an error that names %s", err, file)
			}
		})
	}
}
