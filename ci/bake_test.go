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
