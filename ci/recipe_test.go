package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recipeFixture is a pinned base on a pinned tool. The base's build
// context leaves out its README and its smoke check, and it takes the
// tool's directory as a named context.
func recipeFixture(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"tool/package.toml": "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"tool\"\n",
		"tool/Dockerfile":   "FROM debian@sha256:aaa\n",
		"tool/closure.sh":   "collect\n",
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n" +
			"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool\" }\n",
		"base/Dockerfile":      "FROM debian@sha256:aaa AS build\nCOPY --from=closure closure.sh /\nFROM tool\n",
		"base/base-closure.sh": "seeds\n",
		"base/.dockerignore":   "README.md\nsmoke/\n",
		"base/README.md":       "why\n",
		"base/smoke/base.sh":   "check\n",
	})
}

func recipeOf(t *testing.T, root, name string) string {
	t.Helper()
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := Recipe(root, components, components[name])
	if err != nil {
		t.Fatal(err)
	}
	return recipe
}

func TestTheRecipeCoversWhatTheImageIsBuiltFrom(t *testing.T) {
	write := func(name, text string) func(string) error {
		return func(root string) error { return os.WriteFile(filepath.Join(root, name), []byte(text), 0o644) }
	}
	cases := []struct {
		name    string
		change  func(root string) error
		changes bool
	}{
		{"the Dockerfile", write("base/Dockerfile", "FROM debian@sha256:aaa AS build\nFROM tool\n"), true},
		{"a file in the context", write("base/base-closure.sh", "more seeds\n"), true},
		{"a new file in the context", write("base/extra.conf", "x\n"), true},
		{"a file's mode", func(root string) error { return os.Chmod(filepath.Join(root, "base/base-closure.sh"), 0o755) }, true},
		{"package.toml", write("base/package.toml", "# a comment\n[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n"+
			"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool\" }\n"), true},
		{"the digest of a FROM", write("base/Dockerfile", "FROM debian@sha256:bbb AS build\nCOPY --from=closure closure.sh /\nFROM tool\n"), true},
		{"a dependency's revision", write("tool/package.toml", "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"tool\"\n"), true},
		{"a file the context leaves out", write("base/README.md", "why, at length\n"), false},
		{"the smoke check", write("base/smoke/base.sh", "check more\n"), false},
		{"a dependency's own file", write("tool/closure.sh", "collect more\n"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := recipeFixture(t)
			before := recipeOf(t, root, "base")
			if err := c.change(root); err != nil {
				t.Fatal(err)
			}
			if after := recipeOf(t, root, "base"); (after != before) != c.changes || !strings.HasPrefix(after, "sha256:") {
				t.Errorf("before %s, after %s", before, after)
			}
		})
	}
}

func TestAPinnedComponentStartsFromADigest(t *testing.T) {
	root := recipeFixture(t)
	if err := os.WriteFile(filepath.Join(root, "tool/Dockerfile"), []byte("FROM debian:trixie-slim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Recipes(root, components); err == nil || !strings.Contains(err.Error(), "names no digest") {
		t.Errorf("got %v", err)
	}
}
