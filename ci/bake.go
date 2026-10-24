package main

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// bakeFile is the path of the generated bake file, from the repository
// root. Every image builds through it, on a workstation and in CI.
const bakeFile = "docker-bake.hcl"

// defaultPlatforms are the platforms of an image whose package.toml
// names none.
var defaultPlatforms = []string{"linux/amd64"}

type bakeContext struct{ Name, Value string }

// bakeArg is a build argument with a fixed value.
type bakeArg struct{ Name, Value string }

type bakeTarget struct {
	Name, Context, Dockerfile, Target, Version string
	// Tag is a pinned image's own tag, <version>-<revision>, which also
	// names its branch cache.
	Tag       string
	Pinned    bool
	Platforms []string
	Tags      []string
	Contexts  []bakeContext
	Args      []bakeArg
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
			t, err := newBakeTarget(root, c, image, producer, components)
			if err != nil {
				return nil, err
			}
			targets = append(targets, t)
		}
	}
	return render("docker-bake.hcl.tmpl", map[string]any{"Targets": targets})
}

func newBakeTarget(root string, c *Component, image Image, producer map[string]string, components map[string]*Component) (bakeTarget, error) {
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
		t.Platforms = defaultPlatforms
	}
	for _, name := range slices.Sorted(maps.Keys(contexts)) {
		t.Contexts = append(t.Contexts, bakeContext{name, contexts[name]})
	}
	args, err := versionArgs(c, image, components)
	if err != nil {
		return bakeTarget{}, err
	}
	t.Args = args
	tag := "${VERSION}"
	if c.Pinned() {
		tag = c.PinnedTag()
		t.Tag = tag
	}
	for _, name := range append([]string{image.Name}, image.Aliases...) {
		t.Tags = append(t.Tags, ref(name, tag))
	}
	return t, nil
}

// versionArgs gives the image's versions as build arguments. Each one
// must name a pinned component that c depends on: a tracked component
// has no version until a release gives it one, and a dependency is
// what makes a change to the version build the image again.
func versionArgs(c *Component, image Image, components map[string]*Component) ([]bakeArg, error) {
	var args []bakeArg
	for _, name := range slices.Sorted(maps.Keys(image.Versions)) {
		dep := image.Versions[name]
		other, ok := components[dep]
		switch {
		case name == "VERSION":
			return nil, fmt.Errorf("%s: the image %s: VERSION is the image's own version, so no other version may take its name", c.Name(), image.Name)
		case !ok:
			return nil, fmt.Errorf("%s: the image %s takes %s from %q, which is not a component", c.Name(), image.Name, name, dep)
		case !other.Pinned():
			return nil, fmt.Errorf("%s: the image %s takes %s from %s, which states no version", c.Name(), image.Name, name, dep)
		case !slices.Contains(c.Depends.Components, dep):
			return nil, fmt.Errorf("%s: the image %s takes %s from %s, which is not one of its dependencies", c.Name(), image.Name, name, dep)
		}
		args = append(args, bakeArg{name, other.Package.Version})
	}
	return args, nil
}

// settings lists what the target's build reads from the bake file,
// with no tag and no cache: the recipe of a pinned image covers these,
// and the tags and the cache change no byte of the image.
func (t bakeTarget) settings() string {
	var contexts []string
	for _, c := range t.Contexts {
		contexts = append(contexts, c.Name+"="+c.Value)
	}
	version := "VERSION"
	if t.Pinned {
		version = t.Version
	}
	settings := fmt.Sprintf("%s context=%s dockerfile=%s target=%s platforms=%s contexts=%s VERSION=%s",
		t.Name, t.Context, t.Dockerfile, t.Target, strings.Join(t.Platforms, ","), strings.Join(contexts, ","), version)
	for _, arg := range t.Args {
		settings += " " + arg.Name + "=" + arg.Value
	}
	return settings
}
