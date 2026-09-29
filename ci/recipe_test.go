package main

import (
	"fmt"
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
		"tool/package.toml":        "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"tool\"\ncontext = \"image\"\nfile = \"image/Dockerfile\"\n",
		"tool/image/Dockerfile":    "FROM debian@sha256:aaa\n",
		"tool/image/closure.sh":    "collect\n",
		"tool/other.sh":            "other\n",
		"tool/image/.dockerignore": "",
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n" +
			"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool/image\" }\n",
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
		{"a file's executable bit", func(root string) error { return os.Chmod(filepath.Join(root, "base/base-closure.sh"), 0o755) }, true},
		{"a file's group write bit", func(root string) error { return os.Chmod(filepath.Join(root, "base/base-closure.sh"), 0o664) }, false},
		{"the digest of a COPY source", write("base/Dockerfile", "FROM debian@sha256:aaa AS build\nCOPY --from=closure closure.sh /\nFROM tool\nCOPY --from=alpine@sha256:ccc / /a\n"), true},
		{"the checksum of an ADD from the network", write("base/Dockerfile", "FROM debian@sha256:aaa AS build\nCOPY --from=closure closure.sh /\nFROM tool\nADD --checksum=sha256:ddd https://example.com/x /x\n"), true},
		{"a file its Dockerfile's own ignore file leaves out", func(root string) error {
			if err := os.WriteFile(filepath.Join(root, "base/Dockerfile.dockerignore"), []byte("notes.txt\n"), 0o644); err != nil {
				return err
			}
			before := recipeOf(t, root, "base")
			if err := os.WriteFile(filepath.Join(root, "base/notes.txt"), []byte("more\n"), 0o644); err != nil {
				return err
			}
			if after := recipeOf(t, root, "base"); after != before {
				return fmt.Errorf("a file that Dockerfile.dockerignore leaves out changed the recipe")
			}
			// The Dockerfile's own ignore file replaces .dockerignore,
			// so the README is in the context now.
			return os.WriteFile(filepath.Join(root, "base/README.md"), []byte("why, at length\n"), 0o644)
		}, true},
		{"a file of a named context outside its owner's image context", func(root string) error {
			manifest := "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n" +
				"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool\" }\n"
			if err := os.WriteFile(filepath.Join(root, "base/package.toml"), []byte(manifest), 0o644); err != nil {
				return err
			}
			before := recipeOf(t, root, "base")
			if err := os.WriteFile(filepath.Join(root, "tool/other.sh"), []byte("more\n"), 0o644); err != nil {
				return err
			}
			if after := recipeOf(t, root, "base"); after == before {
				return fmt.Errorf("a file outside tool's image context left the recipe as it was")
			}
			return nil
		}, true},
		{"a named context below a dependency's build context", func(root string) error {
			manifest := "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n" +
				"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool/image/sub\" }\n"
			if err := write("base/package.toml", manifest)(root); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(root, "tool/image/sub"), 0o755); err != nil {
				return err
			}
			before := recipeOf(t, root, "base")
			if err := write("tool/image/sub/x.sh", "x\n")(root); err != nil {
				return err
			}
			if after := recipeOf(t, root, "base"); after == before {
				return fmt.Errorf("a file below tool's build context left the recipe as it was")
			}
			return nil
		}, true},
		{"a dependency's context that its Dockerfile's ignore file shapes", func(root string) error {
			if err := write("tool/image/Dockerfile.dockerignore", "closure.sh\n")(root); err != nil {
				return err
			}
			before := recipeOf(t, root, "base")
			if err := write("tool/image/closure.sh", "collect more\n")(root); err != nil {
				return err
			}
			if after := recipeOf(t, root, "base"); after == before {
				return fmt.Errorf("a file that tool's recipe leaves out left base's recipe as it was")
			}
			return nil
		}, true},
		{"the recipe format", func(root string) error {
			format := recipeFormat
			recipeFormat = "a new format"
			t.Cleanup(func() { recipeFormat = format })
			return nil
		}, true},
		{"package.toml", write("base/package.toml", "# a comment\n[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n"+
			"[[outputs.images]]\nname = \"base\"\ncontexts = { closure = \"tool/image\" }\n"), true},
		{"the digest of a FROM", write("base/Dockerfile", "FROM debian@sha256:bbb AS build\nCOPY --from=closure closure.sh /\nFROM tool\n"), true},
		{"a dependency's revision", write("tool/package.toml", "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"tool\"\ncontext = \"image\"\nfile = \"image/Dockerfile\"\n"), true},
		{"a file the context leaves out", write("base/README.md", "why, at length\n"), false},
		{"the smoke check", write("base/smoke/base.sh", "check more\n"), false},
		{"a dependency's own file", write("tool/image/closure.sh", "collect more\n"), false},
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

func TestARecipeRefusesWhatItCannotPin(t *testing.T) {
	cases := map[string]struct{ dockerfile, wants string }{
		"a FROM with no digest":         {"FROM debian:trixie-slim\n", "debian:trixie-slim, which names no digest"},
		"a COPY source with no digest":  {"FROM tool\nCOPY --from=alpine:3 / /a\n", "alpine:3, which names no digest"},
		"a mount source with no digest": {"FROM tool\nRUN --mount=type=bind,from=busybox:latest,target=/b true\n", "busybox:latest, which names no digest"},
		"an image outside its depends":  {"FROM tool\nCOPY --from=other / /o\n", "other is not in the dependencies of base"},
		"an ADD with no checksum":       {"FROM tool\nADD https://example.com/x /x\n", "https://example.com/x with no --checksum"},
		"an escape directive":           {"# escape=`\nFROM tool\n", "an escape directive"},
		"apt with no snapshot":          {"FROM debian@sha256:aaa AS closure\nRUN apt-get update\nFROM tool\n", "the stage closure runs apt"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := recipeFixture(t)
			other := "[package]\nname = \"other\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"other\"\n"
			for file, text := range map[string]string{"base/Dockerfile": c.dockerfile, "other/package.toml": other, "other/Dockerfile": "FROM scratch\n"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, file), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			components, err := LoadComponents(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Recipe(root, components, components["base"]); err == nil || !strings.Contains(err.Error(), c.wants) {
				t.Errorf("got %v, want an error with %q", err, c.wants)
			}
		})
	}
}
