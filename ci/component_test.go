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

func TestAPackageTomlIsRefused(t *testing.T) {
	cases := map[string]struct{ toml, wants string }{
		"a misspelled key":    {"[package]\nname = \"a\"\nversoin = \"1\"\n", "unknown keys: package.versoin"},
		"a name that differs": {"[package]\nname = \"b\"\n", "is not the directory name"},
		"a pinned version":    {"[package]\nname = \"a\"\nversion = \"1.0\"\nrevision = 1\n", "pinned components are not built yet"},
		"an unknown toolchain": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"zig\"\nrun = \"make\"\n",
			"the toolchain \"zig\""},
		"a job with no command": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\n", "a run command"},
		"two jobs of one name": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\nrun = \"m\"\n[[jobs]]\nname = \"x\"\ntoolchain = \"go\"\nrun = \"m\"\n",
			"two jobs are named"},
		"a job named images": {"[package]\nname = \"a\"\n[[jobs]]\nname = \"images\"\ntoolchain = \"go\"\nrun = \"m\"\n", "belong to the outputs"},
		"two images of one name": {"[package]\nname = \"a\"\n[[outputs.images]]\nname = \"a\"\n[[outputs.images]]\nname = \"b\"\naliases = [\"a\"]\n",
			"two images are named"},
		"a missing dependency": {"[package]\nname = \"a\"\n[depends]\ncomponents = [\"z\"]\n", "which is not a component"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"a/package.toml": c.toml})
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
