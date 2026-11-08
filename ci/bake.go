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

// A bakeContext is one named context of a target. Value is what the
// recipe reads: a directory, an outside image by its digest, or
// target:<image> for an image of the repository. Published names the
// bake variable that can point a base of a pinned component at its
// published image instead, and is empty for every other context.
type bakeContext struct{ Name, Value, Published string }

// publishedVariable names the bake variable that holds the published
// reference of a pinned base, such as PUBLISHED_FFMPEG. `ci bases`
// writes it into the override file when the image passes its checks.
func publishedVariable(image string) string {
	return "PUBLISHED_" + strings.ToUpper(strings.ReplaceAll(image, "-", "_"))
}

// bakeArg is a build argument with a fixed value.
type bakeArg struct{ Name, Value string }

type bakeTarget struct {
	Name, Context, Dockerfile, Target, Version string
	// Description is the image's description, which `docker buildx bake
	// --list=targets` prints. No build reads it, so settings leaves it out.
	Description string
	// Tag is a pinned image's own tag, <version>-<revision>, which also
	// names its branch cache.
	Tag       string
	Pinned    bool
	Platforms []string
	Tags      []string
	Contexts  []bakeContext
	Args      []bakeArg
}

// A cacheTarget builds one of the stages that copy an image's cache
// mount into the build and out of it. It has no tag and reads no
// layer cache from the registry, so on a CI runner, whose builder
// starts empty, each stage runs. It does not set no-cache, because
// BuildKit gives a step that runs without the cache a new, empty
// cache mount.
type cacheTarget struct {
	Name, Context, Dockerfile, Stage, Output string
	Platforms                                []string
	Contexts                                 []bakeContext
}

// cacheDir is the directory, relative to the repository root, that
// holds an image's cache mount between the images job's steps.
func cacheDir(image string) string { return ".image-cache/" + image }

// cacheStages are the stages of a Dockerfile whose image sets
// cache-mount.
var cacheStages = []string{"cache-seed", "cache-export"}

// cacheTargets gives the image's cache-seed and cache-export targets.
// The seed takes the image's cache directory as the context `cache`,
// and the export writes the mount's files into the same directory.
func cacheTargets(root string, t bakeTarget) ([]cacheTarget, error) {
	parsed, err := readDockerfile(filepath.Join(root, t.Context, t.Dockerfile))
	if err != nil {
		return nil, err
	}
	var targets []cacheTarget
	for _, stage := range cacheStages {
		if !slices.ContainsFunc(parsed.Stages, func(s Stage) bool { return s.Name == stage }) {
			return nil, fmt.Errorf("%s sets cache-mount, and %s has no stage %s", t.Name, path.Join(t.Context, t.Dockerfile), stage)
		}
		ct := cacheTarget{
			Name:       t.Name + "-" + stage,
			Context:    t.Context,
			Dockerfile: t.Dockerfile,
			Stage:      stage,
			Output:     "type=cacheonly",
			Platforms:  t.Platforms,
			Contexts:   slices.Clone(t.Contexts),
		}
		if stage == "cache-seed" {
			ct.Contexts = append(ct.Contexts, bakeContext{Name: "cache", Value: cacheDir(t.Name)})
		} else {
			ct.Output = "type=local,dest=" + cacheDir(t.Name)
		}
		targets = append(targets, ct)
	}
	return targets, nil
}

// Bake renders the bake file: one target for each image, named for the
// image. A Dockerfile that builds on another image of the repository
// names it bare, as in FROM mpv, and the target maps that name to the
// other image's target, so the build uses the image of the same commit.
// A Dockerfile names an outside image of toolchains/images.toml bare
// too, as in FROM golang, and the target maps that name to the
// reference and its digest, so each outside image is written once.
func Bake(root string, components map[string]*Component) ([]byte, error) {
	producer := ImageProducers(components)
	var targets []bakeTarget
	var caches []cacheTarget
	for _, c := range dependencyOrder(components) {
		for _, image := range c.Outputs.Images {
			t, err := newBakeTarget(root, c, image, producer, components)
			if err != nil {
				return nil, err
			}
			targets = append(targets, t)
			if image.CacheMount {
				ct, err := cacheTargets(root, t)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", c.Name(), err)
				}
				caches = append(caches, ct...)
			}
		}
	}
	tools, err := readToolchains(root)
	if err != nil {
		return nil, err
	}
	var published []string
	for _, t := range targets {
		for _, c := range t.Contexts {
			if c.Published != "" && !slices.Contains(published, c.Published) {
				published = append(published, c.Published)
			}
		}
	}
	slices.Sort(published)
	var toolchains []bakeContext
	for _, name := range slices.Sorted(maps.Keys(tools.Build)) {
		toolchains = append(toolchains, bakeContext{Name: name, Value: "docker-image://" + tools.Build[name]})
	}
	return render("docker-bake.hcl.tmpl", map[string]any{"Targets": targets, "Caches": caches, "Toolchains": toolchains, "Published": published})
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
	tools, err := toolchainContexts(root, c, image, parsed, producer)
	if err != nil {
		return bakeTarget{}, err
	}
	maps.Copy(contexts, tools)
	t := bakeTarget{
		Name:        image.Name,
		Description: image.Description,
		Context:     context,
		Dockerfile:  filepath.ToSlash(dockerfile),
		Target:      image.Target,
		Pinned:      c.Pinned(),
		Version:     c.Package.Version,
		Platforms:   image.Platforms,
	}
	if len(t.Platforms) == 0 {
		t.Platforms = defaultPlatforms
	}
	for _, name := range slices.Sorted(maps.Keys(contexts)) {
		context := bakeContext{Name: name, Value: contexts[name]}
		if base, ok := strings.CutPrefix(context.Value, "target:"); ok {
			if owner, ok := components[producer[base]]; ok && owner.Pinned() {
				context.Published = publishedVariable(base)
			}
		}
		t.Contexts = append(t.Contexts, context)
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

// hclString quotes plain text as an HCL string. HCL reads a backslash
// and a quote as an escape and the end of the string, and reads ${ and
// %{ as the start of an interpolation and of a directive, so each one
// is escaped.
func hclString(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "${", "$${", "%{", "%%{").Replace(text) + `"`
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
