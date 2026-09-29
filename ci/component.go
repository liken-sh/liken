package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// A Component is a directory with a package.toml. The directory name
// is the component's name, the prefix of its image names, and the
// name of its jobs in the workflow.
//
// A tracked component has no version in its package.toml: its version
// is the repository's release tag. A pinned component states an
// upstream version and a revision, and it publishes under the tag
// <version>-<revision>.
type Component struct {
	// Dir is the component's directory, relative to the repository
	// root, with forward slashes and no trailing slash.
	Dir string `toml:"-"`

	Package struct {
		Name     string `toml:"name"`
		License  string `toml:"license"`
		Version  string `toml:"version"`
		Revision int    `toml:"revision"`
	} `toml:"package"`

	Depends struct {
		// Components names the components whose files go into this
		// component's outputs. A change to one of them is a change to
		// this one.
		Components []string `toml:"components"`
	} `toml:"depends"`

	// Docs is present when the component has a manual in docs/. Every
	// manual builds with the theme in brand/.
	Docs *Docs `toml:"docs"`

	Jobs []Job `toml:"jobs"`

	Outputs Outputs `toml:"outputs"`
}

// Docs places a component's manual in the one site.
type Docs struct {
	// Prefix is the manual's path in the site: liken.sh/<prefix>/. An
	// empty prefix is the site's root, which is liken's own manual.
	Prefix string `toml:"prefix"`
}

// A Job is one check that the gate runs for the component, as one job
// in the component's workflow.
type Job struct {
	Name string `toml:"name"`
	// Toolchain names the setup that the job needs before its command.
	// The workflow template has one block of setup steps for each.
	Toolchain string `toml:"toolchain"`
	// Dir is the job's working directory, relative to the component.
	Dir string `toml:"dir"`
	// Module is the directory of the go.mod that a go or hugo job
	// reads its Go version from, relative to the component. It
	// defaults to Dir.
	Module string `toml:"module"`
	// Run is the command, usually a make target that a workstation
	// runs too. Each line is one command.
	Run string `toml:"run"`
	// Timeout is in minutes.
	Timeout int `toml:"timeout"`
	// Apt names the Ubuntu packages the command needs on the runner.
	Apt []string          `toml:"apt"`
	Env map[string]string `toml:"env"`
	// Coverage names the files, relative to the component, that the
	// job writes for the site's coverage report.
	Coverage []string `toml:"coverage"`
	// Artifacts names files, relative to the component, that the job
	// uploads whether it passes or fails, such as a boot log.
	Artifacts []string `toml:"artifacts"`
	// Skip names the hooks that a prek job leaves out, because another
	// job of the component runs the same command.
	Skip []string `toml:"skip"`
}

// Toolchains are the setups the workflow template knows.
var toolchains = []string{"go", "hugo", "rust", "prek", "os"}

// Outputs are what the component publishes.
type Outputs struct {
	Images []Image `toml:"images"`
	// Deploy is the directory of manifests that ships as an OCI
	// artifact, relative to the component.
	Deploy string `toml:"deploy"`
	// Channel is true for a component that publishes to the release
	// channel at releases.liken.sh.
	Channel bool `toml:"channel"`
	// Exclude names paths, relative to the component, that no output
	// is built from, in addition to the paths in notOutputs that every
	// component leaves out. A change to one runs the component's jobs,
	// but it does not release the component or reach its dependents.
	// A path that ends in a slash is a directory and covers every file
	// under it. Any other path is one file.
	Exclude []string `toml:"exclude"`
}

// An Image is one image on ghcr.io/liken-sh.
type Image struct {
	Name string `toml:"name"`
	// Context and File are relative to the component. File is
	// relative to the component, not to the context.
	Context string `toml:"context"`
	File    string `toml:"file"`
	Target  string `toml:"target"`
	// Platforms defaults to linux/amd64. An image for more than one
	// platform cannot load into the runner's Docker daemon, so it
	// has no smoke check.
	Platforms []string `toml:"platforms"`
	// Contexts are named build contexts, each a directory relative to
	// the repository root, for a Dockerfile's COPY --from=<name>.
	Contexts map[string]string `toml:"contexts"`
	// Aliases are more names for the same image, pushed with the same
	// tags.
	Aliases []string `toml:"aliases"`
	// Smoke is a script, relative to the repository root, that checks
	// the built image. It takes the image reference as its first
	// argument.
	Smoke string `toml:"smoke"`
}

func (c *Component) Name() string { return c.Package.Name }

// Pinned is true for a component that states its own version.
func (c *Component) Pinned() bool { return c.Package.Version != "" }

// PinnedTag is the tag that a pinned component publishes under, such
// as 20260928-1.
func (c *Component) PinnedTag() string {
	return c.Package.Version + "-" + strconv.Itoa(c.Package.Revision)
}

// HasOutputs is true for a component that publishes anything.
func (c *Component) HasOutputs() bool {
	return len(c.Outputs.Images) > 0 || c.Outputs.Deploy != "" || c.Outputs.Channel
}

// DeployPackage is the ghcr package that holds the component's deploy
// artifact.
func (c *Component) DeployPackage() string { return c.Name() + "-deploy" }

// Packages lists every ghcr package that the component pushes to.
func (c *Component) Packages() []string {
	var names []string
	for _, image := range c.Outputs.Images {
		names = append(names, image.Name)
		names = append(names, image.Aliases...)
	}
	if c.Outputs.Deploy != "" {
		names = append(names, c.DeployPackage())
	}
	return names
}

// skipDirs are directories that never hold a component: build output,
// dependencies, and test fixtures.
var skipDirs = map[string]bool{
	".git": true, "dist": true, "target": true, "node_modules": true,
	"testdata": true, "build": true, "public": true, "resources": true,
}

// LoadComponents finds every package.toml under root and reads it.
func LoadComponents(root string) (map[string]*Component, error) {
	components := map[string]*Component{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "package.toml" {
			return nil
		}
		dir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		c, err := readComponent(path, filepath.ToSlash(dir))
		if err != nil {
			return err
		}
		if other, ok := components[c.Name()]; ok {
			return fmt.Errorf("%s and %s both name the component %q", other.Dir, c.Dir, c.Name())
		}
		components[c.Name()] = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return components, validateGraph(components)
}

// readComponent reads one package.toml. A key that the schema does
// not know is an error, so a misspelled field fails here instead of
// reading as absent. That matters most for version: an absent version
// means a tracked component.
func readComponent(path, dir string) (*Component, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Component{Dir: dir}
	meta, err := toml.Decode(string(data), c)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	return c, c.validate(path)
}

func (c *Component) validate(path string) error {
	if c.Name() != filepath.Base(c.Dir) {
		return fmt.Errorf("%s: the name %q is not the directory name %q", path, c.Name(), filepath.Base(c.Dir))
	}
	if err := c.validatePin(path); err != nil {
		return err
	}
	if err := c.validateExclude(path); err != nil {
		return err
	}
	jobs := map[string]bool{}
	for _, job := range c.Jobs {
		if job.Name == "" || (job.Run == "") != (job.Toolchain == "prek") {
			return fmt.Errorf("%s: every job needs a name, and a run command unless prek runs it", path)
		}
		if jobs[job.Name] {
			return fmt.Errorf("%s: two jobs are named %q", path, job.Name)
		}
		jobs[job.Name] = true
		if !slices.Contains(toolchains, job.Toolchain) {
			return fmt.Errorf("%s: job %q: the toolchain %q is not one of %s", path, job.Name, job.Toolchain, strings.Join(toolchains, ", "))
		}
		if len(job.Skip) > 0 && job.Toolchain != "prek" {
			return fmt.Errorf("%s: job %q: only a prek job skips hooks", path, job.Name)
		}
	}
	if jobs["images"] || jobs["publish"] {
		return fmt.Errorf("%s: the job names images and publish belong to the outputs", path)
	}
	images := map[string]bool{}
	for _, image := range c.Outputs.Images {
		for _, name := range append([]string{image.Name}, image.Aliases...) {
			if images[name] {
				return fmt.Errorf("%s: two images are named %q", path, name)
			}
			images[name] = true
		}
	}
	return nil
}

// pinnedVersion is the grammar of a pinned version: the characters of
// an image tag, starting with a letter or a digit, less the hyphen. A
// tag can hold a hyphen, but the version leaves it out, so that the
// last hyphen of <version>-<revision> always starts the revision.
var pinnedVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._]*$`)

// calendarDate is the start of a release version, yyyy.mm.dd.
var calendarDate = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}`)

// validatePin checks the version and the revision together. Either one
// alone is a mistake: a revision with no version would read as a
// tracked component, and a version with no revision has no tag.
//
// A pinned version must not look like a release version, because the
// registry holds both kinds of tag side by side, and a reader tells
// them apart by their shape.
//
// A pinned component publishes images alone. Its recipe hash lives in
// a label of its image, and the plan reads it from there.
func (c *Component) validatePin(path string) error {
	version, revision := c.Package.Version, c.Package.Revision
	switch {
	case version == "" && revision == 0:
		return nil
	case version == "":
		return fmt.Errorf("%s: a revision needs a version; a tracked component has neither", path)
	case revision < 1:
		return fmt.Errorf("%s: a pinned component needs a revision of 1 or more", path)
	case !pinnedVersion.MatchString(version):
		return fmt.Errorf("%s: a version may hold only letters, digits, dots, and underscores, so that the tag <version>-<revision> reads one way, and %q does not", path, version)
	case calendarDate.MatchString(version):
		return fmt.Errorf("%s: the tag %q looks like a release version; write a date as YYYYMMDD", path, c.PinnedTag())
	case len(c.Outputs.Images) == 0 || c.Outputs.Deploy != "" || c.Outputs.Channel:
		return fmt.Errorf("%s: a pinned component publishes images, and nothing else", path)
	}
	return nil
}

// validateExclude checks that each excluded path is a file or a
// directory in the component, in the form that a changed path has
// relative to the component. A path in any other form matches no
// changed file, and the component would release on every change to
// the files it means to leave out.
func (c *Component) validateExclude(path string) error {
	for _, excluded := range c.Outputs.Exclude {
		name := strings.TrimSuffix(excluded, "/")
		if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name {
			return fmt.Errorf("%s: the excluded path %q must be a path inside the component, with no . or .. in it", path, excluded)
		}
		info, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil {
			return fmt.Errorf("%s: the excluded path %q does not exist", path, excluded)
		}
		if info.IsDir() != strings.HasSuffix(excluded, "/") {
			return fmt.Errorf("%s: an excluded directory ends in a slash, and an excluded file does not: %q", path, excluded)
		}
	}
	return nil
}

// validateGraph checks that every dependency exists and that the
// graph has no cycle.
func validateGraph(components map[string]*Component) error {
	for _, c := range sortedComponents(components) {
		for _, dep := range c.Depends.Components {
			other, ok := components[dep]
			if !ok {
				return fmt.Errorf("%s depends on %q, which is not a component", c.Name(), dep)
			}
			// A pinned component's recipe covers the tag of each
			// dependency. A tracked component has no tag until a
			// release gives it one, so it cannot be in a recipe.
			if c.Pinned() && !other.Pinned() {
				return fmt.Errorf("%s is pinned and depends on %s, which is not; a pinned component depends only on pinned components", c.Name(), dep)
			}
		}
	}
	state := map[string]int{}
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("the dependencies form a cycle: %s", strings.Join(append(path, name), " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		for _, dep := range components[name].Depends.Components {
			if err := visit(dep, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for _, c := range sortedComponents(components) {
		if err := visit(c.Name(), nil); err != nil {
			return err
		}
	}
	return nil
}

// sortedComponents lists the components by name, so every output of
// this program is in a stable order.
func sortedComponents(components map[string]*Component) []*Component {
	list := make([]*Component, 0, len(components))
	for _, c := range components {
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b *Component) int { return strings.Compare(a.Name(), b.Name()) })
	return list
}
