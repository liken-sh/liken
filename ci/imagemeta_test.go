package main

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// metadataTree is a repository with each kind of image that the
// publish labels: vulkan, a pinned base on an outside image; mpv, a
// pinned image on vulkan; and operator, a tracked component with an
// image on scratch, one on mpv, and one on an outside image.
func metadataTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"toolchains/package.toml": "[package]\nname = \"toolchains\"\n",
		"toolchains/images.toml": fixtureImages +
			"debian = \"debian:sid-slim@sha256:d1\"\ndistroless = \"gcr.io/distroless/static:nonroot@sha256:d2\"\n",
		"vulkan/package.toml": "[package]\nname = \"vulkan\"\nversion = \"20261101\"\nrevision = 1\n[package.upstream]\nmesa = \"26.0\"\n" +
			"[depends]\ncomponents = [\"toolchains\"]\n[[outputs.images]]\nname = \"vulkan\"\ndescription = \"The Vulkan loader.\"\n",
		"vulkan/Dockerfile": "FROM debian AS closure\nFROM scratch\nCOPY --from=closure /usr /usr\n",
		"mpv/package.toml": "[package]\nname = \"mpv\"\nversion = \"20261101\"\nrevision = 1\n[package.upstream]\nmpv = \"0.41\"\n" +
			"[depends]\ncomponents = [\"vulkan\"]\n[[outputs.images]]\nname = \"mpv\"\ndescription = \"mpv on vulkan.\"\n",
		"mpv/Dockerfile": "FROM vulkan\n",
		"operator/package.toml": `[package]
name = "operator"
license = "MIT"
[docs]
prefix = "operator"
[depends]
components = ["mpv", "toolchains"]
[[outputs.images]]
name = "operator"
description = "The operator."
[[outputs.images]]
name = "operator-player"
description = "The player."
file = "Dockerfile.player"
[[outputs.images]]
name = "operator-cli"
description = "The plugin."
file = "Dockerfile.cli"
`,
		"operator/Dockerfile":        "FROM golang AS build\nFROM scratch\n",
		"operator/Dockerfile.player": "FROM mpv AS player\nFROM player\n",
		"operator/Dockerfile.cli":    "FROM distroless\n",
	})
}

// withOCI adds the OCI keys of an image to the map, and returns it.
func withOCI(m map[string]string, title, description, version, documentation, licenses string) map[string]string {
	m[ociKey("title")] = title
	m[ociKey("description")] = description
	m[ociKey("version")] = version
	m[ociKey("revision")] = fixtureCommit
	m[ociKey("created")] = "2026-10-02T09:30:00Z"
	m[ociKey("source")] = "https://github.com/liken-sh/liken"
	m[ociKey("url")] = "https://liken.sh/"
	m[ociKey("documentation")] = documentation
	m[ociKey("vendor")] = "liken"
	if licenses != "" {
		m[ociKey("licenses")] = licenses
	}
	return m
}

func TestTheMetadataOfAnImage(t *testing.T) {
	pulled := []PublishedBase{
		{Image: "mpv", Reference: "ghcr.io/liken-sh/mpv:20261101-1@sha256:m"},
		{Image: "vulkan", Reference: "ghcr.io/liken-sh/vulkan:20261101-1@sha256:v"},
	}
	tracked := func(m map[string]string, title, description string) map[string]string {
		return withOCI(m, title, description, "2026.10.02-001", "https://liken.sh/operator/", "MIT")
	}
	pinned := func(m map[string]string) map[string]string {
		return withOCI(m, "mpv", "mpv on vulkan.", "20261101-1", "https://github.com/liken-sh/liken/tree/"+fixtureCommit+"/mpv", "")
	}
	cases := []struct {
		name, component, image, version, recipe string
		pulled                                  []PublishedBase
		labels, annotations                     map[string]string
	}{
		{"a tracked image on scratch", "operator", "operator", "2026.10.02-001", "", pulled,
			tracked(map[string]string{}, "operator", "The operator."),
			tracked(map[string]string{}, "operator", "The operator.")},
		{"a tracked image on a pulled pinned base", "operator", "operator-player", "2026.10.02-001", "", pulled,
			tracked(map[string]string{"sh.liken.recipe": "", "sh.liken.upstream.mpv": "", "sh.liken.upstream.mesa": ""}, "operator-player", "The player."),
			tracked(map[string]string{ociKey("base.name"): "ghcr.io/liken-sh/mpv:20261101-1", ociKey("base.digest"): "sha256:m"}, "operator-player", "The player.")},
		{"a tracked image on a pinned base built from the tree", "operator", "operator-player", "2026.10.02-001", "", nil,
			tracked(map[string]string{"sh.liken.recipe": "", "sh.liken.upstream.mpv": "", "sh.liken.upstream.mesa": ""}, "operator-player", "The player."),
			tracked(map[string]string{}, "operator-player", "The player.")},
		{"a pinned image on a pinned base", "mpv", "mpv", "20261101-1", "sha256:r", pulled,
			pinned(map[string]string{"sh.liken.recipe": "sha256:r", "sh.liken.upstream.mpv": "0.41", "sh.liken.upstream.mesa": ""}),
			pinned(map[string]string{ociKey("base.name"): "ghcr.io/liken-sh/vulkan:20261101-1", ociKey("base.digest"): "sha256:v"})},
		{"a tracked image on an outside image", "operator", "operator-cli", "2026.10.02-001", "", pulled,
			tracked(map[string]string{}, "operator-cli", "The plugin."),
			tracked(map[string]string{ociKey("base.name"): "gcr.io/distroless/static:nonroot", ociKey("base.digest"): "sha256:d2"}, "operator-cli", "The plugin.")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := metadataTree(t)
			if c.pulled != nil {
				if err := os.WriteFile(filepath.Join(root, overrideFile), []byte(overrideText(c.pulled)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p, _, components := fixturePublisher(t, root, nil)
			component := components[c.component]
			image := component.Outputs.Images[slices.IndexFunc(component.Outputs.Images, func(i Image) bool { return i.Name == c.image })]
			m, err := p.metadata(component, image, c.version, c.recipe)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m.Labels, c.labels) {
				t.Errorf("labels:\n%v\nwant:\n%v", m.Labels, c.labels)
			}
			if !reflect.DeepEqual(m.Annotations, c.annotations) {
				t.Errorf("annotations:\n%v\nwant:\n%v", m.Annotations, c.annotations)
			}
		})
	}
}

// pinnedLabelsOfFormat holds the labels that the publish writes on mpv
// of metadataTree under each recipe format. A published tag never
// changes, so a change to these labels needs a new recipeFormat, which
// gives every pinned component a new revision, and each tag then
// carries the labels of the code that publishes it.
var pinnedLabelsOfFormat = map[string]map[string]string{
	"2": {
		"org.opencontainers.image.created":       "2026-10-02T09:30:00Z",
		"org.opencontainers.image.description":   "mpv on vulkan.",
		"org.opencontainers.image.documentation": "https://github.com/liken-sh/liken/tree/" + fixtureCommit + "/mpv",
		"org.opencontainers.image.revision":      fixtureCommit,
		"org.opencontainers.image.source":        "https://github.com/liken-sh/liken",
		"org.opencontainers.image.title":         "mpv",
		"org.opencontainers.image.url":           "https://liken.sh/",
		"org.opencontainers.image.vendor":        "liken",
		"org.opencontainers.image.version":       "20261101-1",
		"sh.liken.recipe":                        "sha256:r",
		"sh.liken.upstream.mesa":                 "",
		"sh.liken.upstream.mpv":                  "0.41",
	},
}

func TestAChangeToThePinnedLabelsNeedsANewRecipeFormat(t *testing.T) {
	p, _, components := fixturePublisher(t, metadataTree(t), nil)
	m, err := p.metadata(components["mpv"], components["mpv"].Outputs.Images[0], "20261101-1", "sha256:r")
	if err != nil {
		t.Fatal(err)
	}
	if want := pinnedLabelsOfFormat[recipeFormat]; !maps.Equal(m.Labels, want) {
		t.Errorf("the labels of a pinned image are not the labels of recipe format %s; raise recipeFormat in recipe.go and record the new labels under it:\n%v", recipeFormat, m.Labels)
	}
}

func TestTheDocumentationOfAComponent(t *testing.T) {
	cases := []struct {
		name string
		docs *Docs
		want string
	}{
		{"a manual", &Docs{Prefix: "media"}, "https://liken.sh/media/"},
		{"the manual at the site's root", &Docs{}, "https://liken.sh/docs/"},
		{"no manual", nil, "https://github.com/liken-sh/liken/tree/" + fixtureCommit + "/mpv"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			component := &Component{Dir: "mpv", Docs: c.docs}
			if got := documentationURL(component, fixtureCommit); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// No image of the repository keeps a label of the image it builds on:
// each one writes every label key that its base writes, so a key it
// does not own reads "". The test reads the repository's own
// package.toml files and Dockerfiles. Each base's labels hold the
// labels of the base under it, so one step down the chain covers the
// whole chain.
func TestNoImageOfTheRepositoryKeepsALabelOfItsBase(t *testing.T) {
	components, err := LoadComponents("..")
	if err != nil {
		t.Fatal(err)
	}
	p := Publisher{Root: "..", Components: components, Commit: fixtureCommit, Created: fixtureCreated}
	producer := ImageProducers(components)
	labels := func(c *Component, image Image) map[string]string {
		t.Helper()
		m, err := p.metadata(c, image, "v", "sha256:r")
		if err != nil {
			t.Fatal(err)
		}
		return m.Labels
	}
	for _, c := range sortedComponents(components) {
		for _, image := range c.Outputs.Images {
			parsed, err := readDockerfile(filepath.Join("..", c.Dir, orDefault(image.File, "Dockerfile")))
			if err != nil {
				t.Fatal(err)
			}
			base := parsed.FromImage(image.Target)
			owner, ok := components[producer[base]]
			if _, dir := image.Contexts[base]; dir || !ok {
				continue
			}
			own := labels(c, image)
			for key := range labels(owner, owner.Outputs.Images[slices.IndexFunc(owner.Outputs.Images, func(i Image) bool { return i.Name == base })]) {
				if _, written := own[key]; !written {
					t.Errorf("%s keeps the label %s of %s", image.Name, key, base)
				}
			}
		}
	}
}

func TestQualifiedName(t *testing.T) {
	cases := map[string]string{
		"debian":                     "docker.io/library/debian",
		"golang":                     "docker.io/library/golang",
		"moby/buildkit":              "docker.io/moby/buildkit",
		"gcr.io/distroless/static":   "gcr.io/distroless/static",
		"ghcr.io/liken-sh/corrosion": "ghcr.io/liken-sh/corrosion",
		"localhost/x":                "localhost/x",
		"localhost:5000/x":           "localhost:5000/x",
	}
	for name, want := range cases {
		if got := qualifiedName(name); got != want {
			t.Errorf("qualifiedName(%q) = %q, want %q", name, got, want)
		}
	}
}

// The publish reads the override file that `ci bases` wrote, to name
// the published base that each build pulls.
func TestTheOverrideFileReadsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, overrideFile)
	text := overrideText([]PublishedBase{
		{Image: "media-screen", Reference: "ghcr.io/liken-sh/media-screen:20261101-1@sha256:a"},
		{Image: "mpv", Why: "ghcr.io/liken-sh/mpv:20261101-2 is not published"},
	})
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		want       map[string]string
	}{
		{"a file with one pulled base", path, map[string]string{"PUBLISHED_MEDIA_SCREEN": "ghcr.io/liken-sh/media-screen:20261101-1@sha256:a"}},
		{"no file", filepath.Join(dir, "missing.hcl"), map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readOverride(c.path)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestADockerfileThatWritesALabelFails(t *testing.T) {
	cases := []struct {
		name, text string
		found      bool
	}{
		{"a label", "FROM scratch\nLABEL a=b\n", true},
		{"a label in lowercase", "FROM scratch\nlabel a=b\n", true},
		{"a label over two lines", "FROM scratch\nLABEL a=b \\\n  c=d\n", true},
		{"a maintainer", "FROM scratch\nMAINTAINER someone\n", true},
		{"a command that prints the word", "FROM scratch\nRUN echo LABEL a=b\n", false},
		{"a comment", "FROM scratch\n# LABEL a=b\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := labelInstructions("app/Dockerfile", c.text)
			if found := len(findings) > 0; found != c.found || found && !strings.HasPrefix(findings[0], "app/Dockerfile:2 writes ") {
				t.Errorf("findings %v, want found %v", findings, c.found)
			}
		})
	}
}
