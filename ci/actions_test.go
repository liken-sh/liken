package main

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// everyJobFixture is a repository whose components use every block of
// the workflow templates: an OS with a channel, and an app with a job of
// each toolchain, coverage, artifacts, images, and a deploy artifact.
func everyJobFixture(t *testing.T) (string, map[string]*Component) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"liken/flux/VERSION":     testFlux + "\n",
		"toolchains/images.toml": fixtureBuildKitImages,
		"os/package.toml": `[package]
name = "os"
[[jobs]]
name = "build"
toolchain = "os"
run = "make build"
artifacts = ["boot.log"]
[outputs]
channel = true
`,
		"app/package.toml": `[package]
name = "app"
[docs]
prefix = "app"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test"
coverage = ["coverage.out"]
[[jobs]]
name = "rust"
toolchain = "rust"
run = "cargo test"
[[jobs]]
name = "docs"
toolchain = "hugo"
run = "make docs"
[[jobs]]
name = "prek"
toolchain = "prek"
skip = ["gofmt"]
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "app"
description = "The app image."
`,
		"app/main.go": "package main\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, components
}

// remoteUses matches a step or a job that runs an action from another
// repository: every uses: whose reference does not start with ./.
var remoteUses = regexp.MustCompile(`(?m)^\s*(?:- )?uses: ([^.\s][^\s]*)(.*)$`)

// pinnedUses is the form of a pinned reference: the action, the commit,
// and the release tag that the commit is, as Dependabot reads it.
var pinnedUses = regexp.MustCompile(`^([\w.-]+/[\w.-]+)(?:/[\w./-]+)?@[0-9a-f]{40} # v\d+\.\d+\.\d+$`)

func TestEveryGeneratedActionIsPinnedByCommit(t *testing.T) {
	root, components := everyJobFixture(t)
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	var repositories []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		for _, m := range remoteUses.FindAllStringSubmatch(string(files[name]), -1) {
			ref := m[1] + m[2]
			pinned := pinnedUses.FindStringSubmatch(ref)
			if pinned == nil {
				t.Errorf("%s runs %s, which names no commit and no release", name, ref)
				continue
			}
			repositories = append(repositories, pinned[1])
		}
	}
	slices.Sort(repositories)
	repositories = slices.Compact(repositories)
	if want := pinnedRepositories(); !slices.Equal(repositories, want) {
		t.Errorf("the workflows run %s, and the table pins %s", strings.Join(repositories, " "), strings.Join(want, " "))
	}
}

// testFlux is the flux release of the table, which a fixture writes into
// liken/flux/VERSION, because the generator refuses a flux action that
// differs from the flux of the OS.
var testFlux = func() string { pin, _ := pinOf(fluxActionRepository); return pin.Version }()

// pinnedText is the reference that a generated workflow's text holds
// after uses:, with the comment that names the release.
func pinnedText(action string) string {
	ref, err := pinnedAction(action)
	if err != nil {
		panic(err)
	}
	return ref
}

// pinnedRef is the reference that a parsed workflow holds, without the
// comment.
func pinnedRef(action string) string {
	return strings.SplitN(pinnedText(action), " ", 2)[0]
}

func TestTheChecksRefuseAnActionThatNoCommitNamed(t *testing.T) {
	checkout, _ := pinOf("actions/checkout")
	other := strings.Repeat("a", 40)
	cases := []struct {
		name, line string
		refused    bool
	}{
		{"the table's commit and release", "      - uses: " + pinnedText("actions/checkout"), false},
		{"an action in a subdirectory", "        uses: " + pinnedText("github/codeql-action/init"), false},
		{"a local action", "        uses: ./.github/actions/build-setup", false},
		{"a reusable workflow of the repository", "    uses: ./.github/workflows/component-app.yaml", false},
		{"a line that is not a uses:", "      - run: echo uses: actions/checkout@v7", false},
		{"an image by digest", "      - uses: docker://alpine@sha256:" + strings.Repeat("0", 64), false},
		{"a major tag", "      - uses: actions/checkout@v7", true},
		{"an exact tag", "      - uses: actions/checkout@" + checkout.Version, true},
		{"a branch", "      - uses: actions/checkout@main", true},
		{"a commit with no release", "      - uses: actions/checkout@" + checkout.Commit, true},
		{"a commit that is not the table's", "      - uses: actions/checkout@" + other + " # " + checkout.Version, true},
		{"a release that is not the table's", "      - uses: actions/checkout@" + checkout.Commit + " # v1.0.0", true},
		{"a repository that the table lacks", "      - uses: example/action@" + other + " # v1.0.0", true},
		{"an image by tag", "      - uses: docker://alpine:3", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unpinnedAction(tc.line) != ""; got != tc.refused {
				t.Errorf("%q: refused %v, want %v", tc.line, got, tc.refused)
			}
		})
	}
}

func TestTheChecksReadEveryWorkflowAndCompositeAction(t *testing.T) {
	root := writeTree(t, map[string]string{
		".github/workflows/cert.yaml":            "jobs:\n  cert:\n    steps:\n      - uses: actions/checkout@v7\n",
		".github/workflows/ci.yaml":              "jobs:\n  ci:\n    steps:\n      - uses: " + pinnedText("actions/checkout") + "\n",
		".github/actions/build-setup/action.yml": "runs:\n  steps:\n    - uses: actions/cache@v6\n",
		"docs/example.yaml":                      "steps:\n  - uses: actions/checkout@v7\n",
	})
	findings, err := checkActions(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range findings {
		files = append(files, strings.SplitN(f, ":", 2)[0])
	}
	want := []string{".github/actions/build-setup/action.yml", ".github/workflows/cert.yaml"}
	if !slices.Equal(files, want) {
		t.Errorf("findings %v, want one in each of %v", findings, want)
	}
}

func TestTheGeneratorRefusesAFluxActionOtherThanTheOSFlux(t *testing.T) {
	root := writeTree(t, map[string]string{
		"liken/flux/VERSION": "v1.0.0\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "--bump fluxcd/flux2") {
		t.Errorf("got %v", err)
	}
}
