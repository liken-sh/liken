package main

import (
	"strings"
	"testing"
)

func TestTheBakeFileBuildsEachConsumerOnTheBaseInTheTree(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":   "FROM debian@sha256:aaa AS closure\nFROM scratch\nCOPY --from=closure /out /\n",
		"app/package.toml": "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\"]\n" +
			"[[outputs.images]]\nname = \"app\"\ntarget = \"app\"\naliases = [\"app-sidecar\"]\ncontexts = { brand = \"brand\" }\n" +
			"[[outputs.images]]\nname = \"app-browser\"\ncontext = \"browser\"\nfile = \"browser/Dockerfile\"\nplatforms = [\"linux/amd64\", \"linux/arm64\"]\n",
		"app/Dockerfile":         "FROM golang AS build\nFROM base AS app\nCOPY --from=brand fonts /f\n",
		"app/browser/Dockerfile": "FROM scratch\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"targets = [\n    \"base\",\n    \"app\",\n    \"app-browser\",\n  ]",
		`target "base" {
  context    = "base"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/base:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/base:buildcache", "type=registry,ref=ghcr.io/liken-sh/base:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "base" ? ["type=registry,ref=ghcr.io/liken-sh/base:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "base" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/base:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
  )
}`,
		`  tags       = ["ghcr.io/liken-sh/app:${VERSION}", "ghcr.io/liken-sh/app-sidecar:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/app:buildcache"]
  cache-to   = CACHE_WRITE == "app" ? ["type=registry,ref=ghcr.io/liken-sh/app:buildcache,mode=max,ignore-error=true"] : []
}`,
		`target "app" {
  context    = "app"
  dockerfile = "Dockerfile"
  target     = "app"
  platforms  = ["linux/amd64"]
  contexts = {
    "base" = "target:base"
    "brand" = "brand"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/app:${VERSION}", "ghcr.io/liken-sh/app-sidecar:${VERSION}"]`,
		`target "app-browser" {
  context    = "app/browser"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the bake file lacks:\n%s\n\nin:\n%s", want, text)
		}
	}
}

func TestTheBakeFileNeedsEachDockerfile(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bake(root, components); err == nil {
		t.Error("the bake file rendered with no Dockerfile")
	}
}

func versionsTree(t *testing.T, app string) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":    "FROM scratch\n",
		"loose/package.toml": "[package]\nname = \"loose\"\n[[outputs.images]]\nname = \"loose\"\n",
		"loose/Dockerfile":   "FROM scratch\n",
		"app/package.toml":   app,
		"app/Dockerfile":     "FROM debian AS closure\nARG SNAPSHOT\n",
		"other/package.toml": "[package]\nname = \"other\"\nversion = \"20261001\"\nrevision = 1\n[[outputs.images]]\nname = \"other\"\n",
		"other/Dockerfile":   "FROM scratch\n",
	})
}

func TestTheBakeFilePassesAPinnedDependencysVersionAsABuildArgument(t *testing.T) {
	root := versionsTree(t, "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\"]\n"+
		"[[outputs.images]]\nname = \"app\"\nversions = { SNAPSHOT = \"base\" }\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "  args = {\n    VERSION = VERSION\n    SNAPSHOT = \"20260928\"\n  }\n"
	if !strings.Contains(string(out), want) {
		t.Errorf("the bake file lacks:\n%s\n\nin:\n%s", want, out)
	}
}

func TestTheBakeFileRefusesAVersionArgument(t *testing.T) {
	for name, versions := range map[string]string{
		"an unknown component": `{ SNAPSHOT = "missing" }`,
		"a tracked component":  `{ SNAPSHOT = "loose" }`,
		"no dependency":        `{ SNAPSHOT = "other" }`,
		"the name VERSION":     `{ VERSION = "base" }`,
	} {
		t.Run(name, func(t *testing.T) {
			root := versionsTree(t, "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\", \"loose\"]\n"+
				"[[outputs.images]]\nname = \"app\"\nversions = "+versions+"\n")
			components, err := LoadComponents(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Bake(root, components); err == nil {
				t.Errorf("the bake file rendered with versions = %s", versions)
			}
		})
	}
}

// toolchainImages is an images.toml with one build image and one run
// image.
const toolchainImages = "[build]\ngolang = \"golang:1.27.3-bookworm@sha256:aaa\"\n[run]\ncurl = \"curlimages/curl:8.22.0\"\n"

func toolchainTree(t *testing.T, files map[string]string) string {
	t.Helper()
	tree := map[string]string{
		"toolchains/package.toml": "[package]\nname = \"toolchains\"\n",
		"toolchains/images.toml":  toolchainImages,
		"app/Dockerfile":          "FROM --platform=$BUILDPLATFORM golang AS build\nFROM scratch\nCOPY --from=build /app /app\n",
	}
	for name, text := range files {
		tree[name] = text
	}
	return writeTree(t, tree)
}

func TestTheBakeFileResolvesAToolchainImageToItsDigest(t *testing.T) {
	root := toolchainTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"app\"\ncontexts = { brand = \"brand\" }\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "  contexts = {\n    \"brand\" = \"brand\"\n    \"golang\" = \"docker-image://golang:1.27.3-bookworm@sha256:aaa\"\n  }\n"
	if !strings.Contains(string(out), want) {
		t.Errorf("the bake file lacks:\n%s\n\nin:\n%s", want, out)
	}
}

func TestTheBakeFileGivesOverlaysEveryToolchainImage(t *testing.T) {
	root := toolchainTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"app\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "target \"toolchains\" {\n  contexts = {\n    \"golang\" = \"docker-image://golang:1.27.3-bookworm@sha256:aaa\"\n  }\n}\n"
	if !strings.Contains(string(out), want) {
		t.Errorf("the bake file lacks:\n%s\n\nin:\n%s", want, out)
	}
	_, group, _ := strings.Cut(string(out), "group \"default\" {")
	group, _, _ = strings.Cut(group, "}")
	if strings.Contains(group, "\"toolchains\"") {
		t.Errorf("the default group builds the toolchains target:\n%s", group)
	}
}

func TestTheBakeFileRefusesAToolchainImage(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		wants string
	}{
		"with no dependency on toolchains": {map[string]string{
			"app/package.toml": "[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\n",
		}, "app does not depend on toolchains"},
		"with no digest": {map[string]string{
			"toolchains/images.toml": "[build]\ngolang = \"golang:1.27.3-bookworm\"\n",
			"app/package.toml":       "[package]\nname = \"app\"\n[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"app\"\n",
		}, "golang names no digest"},
		"with the name of an image of the repository": {map[string]string{
			"app/package.toml":    "[package]\nname = \"app\"\n[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"app\"\n",
			"golang/package.toml": "[package]\nname = \"golang\"\n[[outputs.images]]\nname = \"golang\"\n",
			"golang/Dockerfile":   "FROM scratch\n",
		}, "golang is both an image of the component golang and an entry of toolchains/images.toml"},
		"with an unknown table": {map[string]string{
			"toolchains/images.toml": toolchainImages + "[other]\nx = \"y\"\n",
			"app/package.toml":       "[package]\nname = \"app\"\n[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"app\"\n",
		}, "unknown keys: other"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := toolchainTree(t, c.files)
			components, err := LoadComponents(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Bake(root, components); err == nil || !strings.Contains(err.Error(), c.wants) {
				t.Errorf("got %v, want an error with %q", err, c.wants)
			}
		})
	}
}
