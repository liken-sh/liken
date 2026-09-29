package main

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
)

// bakeFile is the path of the generated bake file, from the repository
// root. Every image builds through it, on a workstation and in CI.
const bakeFile = "docker-bake.hcl"

type bakeContext struct{ Name, Value string }

type bakeTarget struct {
	Name, Context, Dockerfile, Target, Version string
	Pinned                                     bool
	Platforms, Tags                            []string
	Contexts                                   []bakeContext
}

// Bake renders the bake file: one target for each image, named for the
// image. A Dockerfile that builds on another image of the repository
// names it bare, as in FROM mpv, and the target maps that name to the
// other image's target, so the build uses the image of the same commit.
func Bake(root string, components map[string]*Component) ([]byte, error) {
	producer := ImageProducers(components)
	var targets []bakeTarget
	for _, c := range dependencyOrder(components) {
		for _, image := range c.Outputs.Images {
			t, err := newBakeTarget(root, c, image, producer)
			if err != nil {
				return nil, err
			}
			targets = append(targets, t)
		}
	}
	return render("docker-bake.hcl.tmpl", map[string]any{"Targets": targets})
}

func newBakeTarget(root string, c *Component, image Image, producer map[string]string) (bakeTarget, error) {
	context := path.Join(c.Dir, image.Context)
	file := path.Join(c.Dir, orDefault(image.File, "Dockerfile"))
	dockerfile, err := filepath.Rel(context, file)
	if err != nil {
		return bakeTarget{}, err
	}
	parsed, err := readDockerfile(filepath.Join(root, file))
	if err != nil {
		return bakeTarget{}, fmt.Errorf("%s: %w", c.Name(), err)
	}
	contexts := map[string]string{}
	maps.Copy(contexts, image.Contexts)
	for _, base := range parsed.Bases(image, producer) {
		contexts[base] = "target:" + base
	}
	t := bakeTarget{
		Name:       image.Name,
		Context:    context,
		Dockerfile: filepath.ToSlash(dockerfile),
		Target:     image.Target,
		Pinned:     c.Pinned(),
		Version:    c.Package.Version,
		Platforms:  image.Platforms,
	}
	if len(t.Platforms) == 0 {
		t.Platforms = []string{"linux/amd64"}
	}
	for _, name := range slices.Sorted(maps.Keys(contexts)) {
		t.Contexts = append(t.Contexts, bakeContext{name, contexts[name]})
	}
	tag := "${VERSION}"
	if c.Pinned() {
		tag = c.PinnedTag()
	}
	for _, name := range append([]string{image.Name}, image.Aliases...) {
		t.Tags = append(t.Tags, ref(name, tag))
	}
	return t, nil
}
