package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// toolchainsComponent owns the outside images that the repository
// builds from and runs, in imagesFile. A component whose Dockerfile
// names one of its build images depends on it, so a change to an
// entry releases that component.
const (
	toolchainsComponent = "toolchains"
	imagesFile          = "toolchains/images.toml"
)

// Toolchains is imagesFile. Build maps the bare name that a Dockerfile
// names, as in FROM golang, to the image reference with its digest.
// Run maps a name to the reference, by tag, that the manifests, the
// guides, the skills, and the tests name.
type Toolchains struct {
	Build map[string]string `toml:"build"`
	Run   map[string]string `toml:"run"`
}

// readToolchains reads imagesFile. A tree with no imagesFile has no
// toolchain images, and every bare name in a Dockerfile is a stage or
// an image of the repository.
//
// Each build image needs a digest, because the bake file builds from
// the reference as it is written, and a tag can move to other bytes
// with no change to the tree.
func readToolchains(root string) (Toolchains, error) {
	var t Toolchains
	data, err := os.ReadFile(filepath.Join(root, imagesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	meta, err := toml.Decode(string(data), &t)
	if err != nil {
		return t, fmt.Errorf("%s: %w", imagesFile, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return t, fmt.Errorf("%s: unknown keys: %s", imagesFile, strings.Join(keys, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(t.Build)) {
		if ref := parseImageRef(t.Build[name]); ref.Digest == "" || ref.Tag == "" {
			return t, fmt.Errorf("%s: the build image %s names no digest or no tag; a Dockerfile builds from it by its digest", imagesFile, name)
		}
	}
	return t, nil
}

// toolchainContexts maps each name in the Dockerfile that is a build
// image of imagesFile to the bake context that resolves it. A named
// directory context of the image keeps its name. The whole file counts,
// not only the stages that the target reaches, so that a recipe can
// resolve every name it hashes.
func toolchainContexts(root string, c *Component, image Image, parsed Dockerfile, producer map[string]string) (map[string]string, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return nil, err
	}
	contexts := map[string]string{}
	for _, name := range append(slices.Clone(parsed.From), parsed.Sources...) {
		reference, ok := tools.Build[name]
		if _, dir := image.Contexts[name]; dir || !ok {
			continue
		}
		if owner, ok := producer[name]; ok {
			return nil, fmt.Errorf("%s is both an image of the component %s and an entry of %s; a bare name must name one image", name, owner, imagesFile)
		}
		if !slices.Contains(c.Depends.Components, toolchainsComponent) {
			return nil, fmt.Errorf("%s: the image %s builds on %s from %s, and %s does not depend on %s", c.Name(), image.Name, name, imagesFile, c.Name(), toolchainsComponent)
		}
		contexts[name] = "docker-image://" + reference
	}
	return contexts, nil
}

// An imageReference is one image reference split into its parts:
// gcr.io/distroless/static:nonroot@sha256:... has the name
// gcr.io/distroless/static, the tag nonroot, and the digest.
type imageReference struct{ Name, Tag, Digest string }

func parseImageRef(s string) imageReference {
	var r imageReference
	s, r.Digest, _ = strings.Cut(s, "@")
	slash := strings.LastIndex(s, "/")
	if colon := strings.LastIndex(s, ":"); colon > slash {
		s, r.Tag = s[:colon], s[colon+1:]
	}
	r.Name = s
	return r
}
