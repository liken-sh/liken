package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files under a temporary root, each path relative to
// it, and returns the root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheComponentsLoadFromEveryPackageToml(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": `[package]
name = "base"
`,
		"app/package.toml": `[package]
name = "app"
[depends]
components = ["base"]
[docs]
prefix = "app"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test"
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "app"
aliases = ["app-sidecar"]
`,
		"app/testdata/package.toml": `not toml at all`,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	app := components["app"]
	if len(components) != 2 || app.Dir != "app" || app.Docs.Prefix != "app" {
		t.Fatalf("loaded %v", components)
	}
	if got := strings.Join(app.Packages(), " "); got != "app app-sidecar app-deploy" {
		t.Errorf("packages %q", got)
	}
}

// pinnedImage is the outputs table of a pinned component with one image.
const pinnedImage = "[[outputs.images]]\nname = \"a\"\n"

func TestAPinnedComponentPublishesUnderItsVersionAndRevision(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/package.toml": "[package]\nname = \"a\"\nversion = \"20260928\"\nrevision = 2\n" + pinnedImage,
		"b/package.toml": "[package]\nname = \"b\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := components["a"], components["b"]; !a.Pinned() || a.PinnedTag() != "20260928-2" || b.Pinned() {
		t.Errorf("a pinned %v at %s, b pinned %v", a.Pinned(), a.PinnedTag(), b.Pinned())
	}
}

func TestAPinnedComponentDependsOnlyOnPinnedComponents(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/package.toml": "[package]\nname = \"a\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"b\"]\n" + pinnedImage,
		"b/package.toml": "[package]\nname = \"b\"\n",
	})
	_, err := LoadComponents(root)
	if err == nil || !strings.Contains(err.Error(), "a pinned component depends only on pinned components") {
		t.Fatalf("got %v", err)
	}
}

func TestAPackageTomlIsRefused(t *testing.T) {
	cases := map[string]struct{ toml, wants string }{
		"a misspelled key":           {"[package]\nname = \"a\"\nversoin = \"1\"\n", "unknown keys: package.versoin"},
		"a name that differs":        {"[package]\nname = \"b\"\n", "is not the directory name"},
		"a revision with no version": {"[package]\nname = \"a\"\nrevision = 1\n" + pinnedImage, "a revision needs a version"},
		"a version with no revision": {"[package]\nname = \"a\"\nversion = \"20260928\"\n" + pinnedImage, "a revision of 1 or more"},
		"a version a tag cannot hold": {"[package]\nname = \"a\"\nversion = \"1.0/2\"\nrevision = 1\n" + pinnedImage,
			"may hold only letters, digits, dots, and underscores"},
		"a version with a hyphen": {"[package]\nname = \"a\"\nversion = \"1.0-rc1\"\nrevision = 1\n" + pinnedImage,
			"may hold only letters, digits, dots, and underscores"},
		"a version that looks like a release": {"[package]\nname = \"a\"\nversion = \"2026.09.28\"\nrevision = 1\n" + pinnedImage,
			"looks like a release version"},
		"a pinned deploy artifact": {"[package]\nname = \"a\"\nversion = \"20260928\"\nrevision = 1\n[outputs]\ndeploy = \"deploy\"\n" + pinnedImage,
			"publishes images, and nothing else"},
		"an unknown toolchain": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"zig\"\nrun = \"make\"\n",
			"the toolchain \"zig\""},
		"a job with no command": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\n", "a run command"},
		"two jobs of one name": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\nrun = \"m\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\nrun = \"m\"\n",
			"two jobs are named"},
		"a job named images": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"images\"\ntoolchain = \"go\"\nrun = \"m\"\n", "belong to the outputs"},
		"two images of one name": {"[package]\nname = \"a\"\n[[outputs.images]]\nname = \"a\"\n[[outputs.images]]\nname = \"b\"\naliases = [\"a\"]\n",
			"two images are named"},
		"an excluded path that does not exist": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"monitoring/\"]\n",
			"the excluded path \"monitoring/\" does not exist"},
		"an absolute excluded path": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"/etc/\"]\n",
			"a path inside the component"},
		"an excluded path outside the component": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"../a/package.toml\"]\n",
			"a path inside the component"},
		"an excluded path that is not clean": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"./package.toml\"]\n",
			"a path inside the component"},
		"an excluded file named as a directory": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"package.toml/\"]\n",
			"an excluded directory ends in a slash"},
		"the component itself excluded": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"./\"]\n",
			"a path inside the component"},
		"an excluded deploy directory": {"[package]\nname = \"a\"\n[outputs]\ndeploy = \"deploy\"\nexclude = [\"deploy/\"]\n",
			"the deploy artifact holds"},
		"an excluded path in the deploy directory": {"[package]\nname = \"a\"\n[outputs]\ndeploy = \"deploy\"\nexclude = [\"deploy/base/\"]\n",
			"the deploy artifact holds"},
		"an excluded directory that holds the deploy directory": {"[package]\nname = \"a\"\n[outputs]\ndeploy = \"deploy/base\"\nexclude = [\"deploy/\"]\n",
			"the deploy artifact holds"},
		"an excluded path that an image's build context sends": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"cmd/\"]\n[[outputs.images]]\nname = \"a\"\n",
			"the image a receives"},
		"an excluded path that a named build context sends": {"[package]\nname = \"a\"\n[outputs]\nexclude = [\"cmd/main.go\"]\n[[outputs.images]]\nname = \"a\"\ncontext = \"deploy\"\ncontexts = { src = \"a/cmd\" }\n",
			"the image a receives"},
		"a missing dependency": {"[package]\nname = \"a\"\n[depends]\ncomponents = [\"z\"]\n", "which is not a component"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"a/package.toml":              c.toml,
				"a/deploy/base/operator.yaml": "",
				"a/cmd/main.go":               "",
			})
			_, err := LoadComponents(root)
			if err == nil || !strings.Contains(err.Error(), c.wants) {
				t.Fatalf("got %v, want an error with %q", err, c.wants)
			}
		})
	}
}

func TestACycleIsRefused(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/package.toml": "[package]\nname = \"a\"\n[depends]\ncomponents = [\"b\"]\n",
		"b/package.toml": "[package]\nname = \"b\"\n[depends]\ncomponents = [\"a\"]\n",
	})
	_, err := LoadComponents(root)
	if err == nil || !strings.Contains(err.Error(), "a -> b -> a") {
		t.Fatalf("got %v", err)
	}
}

func TestTwoDirectoriesCannotNameOneComponent(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/package.toml":   "[package]\nname = \"a\"\n",
		"x/a/package.toml": "[package]\nname = \"a\"\n",
	})
	_, err := LoadComponents(root)
	if err == nil || !strings.Contains(err.Error(), "both name the component") {
		t.Fatalf("got %v", err)
	}
}

// A deploy/ directory is where a component keeps its manifests, but
// only the deploy key publishes them. liken keeps its monitoring
// component in deploy/ and publishes to the channel alone.
func TestADeployDirectoryWithNoDeployKeyPublishesNoArtifact(t *testing.T) {
	root := writeTree(t, map[string]string{
		"os/package.toml":                         "[package]\nname = \"os\"\n[outputs]\nchannel = true\nexclude = [\"deploy/monitoring/\"]\n",
		"os/deploy/monitoring/kustomization.yaml": "",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := components["os"].Packages(); len(got) != 0 {
		t.Errorf("packages %v", got)
	}
}
