package main

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// packageFile is the file that makes a directory a component.
const packageFile = "package.toml"

// A Component is a directory with a package.toml. The directory name
// is the component's name, the prefix of its image names, and the
// name of its jobs in the workflow.
//
// A tracked component has no version in its package.toml, and its
// version is the repository's release tag. A pinned component sets
// its own version and a revision, and publishes under the tag
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
		// Upstream maps an upstream project to the release of it in a
		// pinned component's images, such as indi = "2.2.5". A pinned
		// version is often a snapshot date, which does not tell a reader
		// which releases an image contains, so the publish writes each
		// entry as the image label sh.liken.upstream.<name>.
		Upstream map[string]string `toml:"upstream"`
	} `toml:"package"`

	Depends struct {
		// Components lists the components whose files are built into
		// this component's outputs. A change to one of them counts as a
		// change to this component.
		Components []string `toml:"components"`
	} `toml:"depends"`

	// Docs is set when the component has a manual in docs/.
	Docs *Docs `toml:"docs"`

	Jobs []Job `toml:"jobs"`

	Outputs Outputs `toml:"outputs"`
}

// Docs sets where a component's manual is published on the liken.sh site.
type Docs struct {
	// Prefix is the manual's path on the site: liken.sh/<prefix>/. An
	// empty prefix puts the manual at the root of the site, which is
	// the manual of the `liken` OS.
	Prefix string `toml:"prefix"`
}

// Outputs lists what the component publishes.
type Outputs struct {
	Images []Image `toml:"images"`
	// Deploy is a directory of manifests, relative to the component,
	// that is published as an OCI artifact.
	Deploy string `toml:"deploy"`
	// Channel is true for a component that publishes to the release
	// channel at releases.liken.sh.
	Channel bool `toml:"channel"`
	// Exclude lists paths, relative to the component, that no output is
	// built from, in addition to notOutputs. A change to an excluded
	// path still runs the component's jobs, but it does not release the
	// component or count as a change to its dependents. A path that ends
	// in a slash is a directory, and any other path is a file. The
	// loader refuses a path that the deploy artifact or an image's build
	// reads, because those paths are outputs.
	Exclude []string `toml:"exclude"`
	// Timeout is the publish job's limit in minutes, for a publish that
	// can take longer than the default, such as one that compiles a
	// toolchain from source on a cache miss. The job keeps the default
	// when the default is larger.
	Timeout int `toml:"timeout"`
}

// An Image is one image on ghcr.io/liken-sh.
type Image struct {
	Name string `toml:"name"`
	// Description is one line that describes the image. The publish
	// writes it to org.opencontainers.image.description, as a label
	// and as an annotation of the index and the manifest, and ghcr.io
	// shows it on the package page. It is plain text, so it must not
	// contain Markdown. Aliases use the same description because they
	// push the same digest.
	Description string `toml:"description"`
	// Context and File are both relative to the component. File is not
	// relative to Context.
	Context string `toml:"context"`
	File    string `toml:"file"`
	Target  string `toml:"target"`
	// Platforms defaults to linux/amd64. An image for more than one
	// platform cannot be loaded into the runner's Docker daemon, so it
	// gets no smoke check.
	Platforms []string `toml:"platforms"`
	// Contexts maps the name of a build context to a directory
	// relative to the repository root. A Dockerfile reads it with
	// COPY --from=<name>.
	Contexts map[string]string `toml:"contexts"`
	// Versions maps a build argument to a pinned component that this
	// one depends on, and the build passes that component's version as
	// the argument. For example, an image that installs from vulkan's
	// Debian snapshot gets vulkan's version, the snapshot date, so the
	// date is written in one place.
	Versions map[string]string `toml:"versions"`
	// Aliases are other names that the same image is pushed under,
	// with the same tags.
	Aliases []string `toml:"aliases"`
	// Timeout is the limit in minutes of the jobs that build the image,
	// for a build that compiles a toolchain from source on a cache miss.
	// It applies to the images job and to the publish job, which builds
	// the image again. A job keeps the default when the default is
	// larger.
	Timeout int `toml:"timeout"`
	// Smoke is a script, relative to the repository root, that checks
	// the built image. It receives the image reference as its first
	// argument.
	Smoke string `toml:"smoke"`
	// CacheMount is true for an image whose Dockerfile uses a cache
	// mount, such as a compiler cache, that CI keeps in the Actions
	// cache between runs. The Dockerfile must have a stage cache-seed
	// that copies the build context `cache` into the mount, and a stage
	// cache-export that outputs the mount's files. The images job builds
	// cache-seed before the image and cache-export after it.
	CacheMount bool `toml:"cache-mount"`
}

func (c *Component) Name() string { return c.Package.Name }

func (c *Component) Pinned() bool { return c.Package.Version != "" }

// PinnedTag is the tag that a pinned component publishes under, such
// as 20260928-1.
func (c *Component) PinnedTag() string {
	return c.Package.Version + "-" + strconv.Itoa(c.Package.Revision)
}

func (c *Component) HasOutputs() bool {
	return len(c.Outputs.Images) > 0 || c.Outputs.Deploy != "" || c.Outputs.Channel
}

// DeployPackage is the name of the ghcr package that stores the
// component's deploy artifact.
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

// skipDirs are directory names that LoadComponents does not search for
// package.toml files: build output, dependencies, download caches, and
// test fixtures.
var skipDirs = map[string]bool{
	".git": true, "dist": true, "target": true, "node_modules": true,
	"testdata": true, "build": true, "public": true, "resources": true,
	".cache": true,
}

// LoadComponents reads every package.toml under root and adds the
// CodeQL jobs of each component. No package.toml lists the C, C++, or
// Python files that CodeQL analyzes, so the loader reads them from
// git's list of the repository's files. A directory walk would also
// find the upstream sources that a fetch script extracts into ignored
// directories, and two checkouts of one commit would then generate
// different workflows.
func LoadComponents(root string) (map[string]*Component, error) {
	components := map[string]*Component{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		// A directory below the root that has its own .git is another
		// checkout, such as a git worktree. Its package.toml files
		// repeat the names of the components in this checkout, and the
		// duplicate check would fail.
		if d.IsDir() && path != root {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			}
		}
		if d.IsDir() || d.Name() != packageFile {
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
	if err := validateGraph(components); err != nil {
		return nil, err
	}
	sources, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	owned := map[string][]string{}
	for _, file := range sources {
		if !languageSource(languageC, file) && !languageSource(languagePython, file) {
			continue
		}
		if owner := ownerOf(components, file); owner != "" {
			owned[owner] = append(owned[owner], strings.TrimPrefix(file, components[owner].Dir+"/"))
		}
	}
	for _, c := range components {
		c.Jobs = append(c.Jobs, codeqlJobs(c, owned[c.Name()])...)
	}
	if err := validateReads(root, components); err != nil {
		return nil, err
	}
	return components, validateExcluded(root, components)
}

// readComponent reads one package.toml. It fails on a key that the
// schema does not define, so a misspelled field is an error and not a
// missing value. This matters most for version, because a missing
// version makes the component tracked.
func readComponent(path, dir string) (*Component, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Component{Dir: dir}
	meta, err := toml.Decode(string(data), c)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
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
	grype := 0
	for _, job := range c.Jobs {
		if jobs[job.Name] {
			return fmt.Errorf("%s: two jobs are named %q", path, job.Name)
		}
		jobs[job.Name] = true
		if job.Grype {
			grype++
		}
		if err := c.validateJob(path, job); err != nil {
			return err
		}
	}
	// Code scanning replaces a category's results with each upload, so
	// two jobs that upload under one category would close each other's
	// alerts.
	if grype > 1 {
		return fmt.Errorf("%s: two jobs upload a grype scan under the category grype/%s", path, c.Name())
	}
	for _, job := range c.Jobs {
		if job.Shards > 1 && jobs[shardsName(job.Name)] {
			return fmt.Errorf("%s: the job %q has the name that the shards of job %q use", path, shardsName(job.Name), job.Name)
		}
	}
	if jobs["images"] || jobs["publish"] {
		return fmt.Errorf("%s: a job cannot be named images or publish, because the outputs use those names", path)
	}
	images := map[string]bool{}
	for _, image := range c.Outputs.Images {
		if err := validateImage(path, image); err != nil {
			return err
		}
		for _, name := range append([]string{image.Name}, image.Aliases...) {
			if images[name] {
				return fmt.Errorf("%s: two images are named %q", path, name)
			}
			images[name] = true
		}
	}
	return nil
}

// pinnedVersion allows the characters of an image tag except the
// hyphen, so the last hyphen of the tag <version>-<revision> always
// starts the revision.
var pinnedVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._]*$`)

// calendarDate matches the start of a release version, yyyy.mm.dd.
var calendarDate = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}`)

var upstreamName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// validatePin checks the version and the revision together. Setting
// only one of them is an error: a revision with no version would make
// the component tracked, and a version with no revision gives no tag.
//
// A pinned version must not look like a release version. The registry
// has both kinds of tag, and people tell them apart by their shape.
//
// A pinned component publishes images only, because the plan reads
// its recipe hash from the sh.liken.recipe label of its first image.
func (c *Component) validatePin(path string) error {
	version, revision := c.Package.Version, c.Package.Revision
	for _, name := range slices.Sorted(maps.Keys(c.Package.Upstream)) {
		switch {
		case version == "":
			return fmt.Errorf("%s: only a pinned component can list upstream versions, because the version of a tracked component is its release", path)
		case !upstreamName.MatchString(name):
			return fmt.Errorf("%s: an upstream name may hold only lowercase letters, digits, and hyphens, because it ends the label sh.liken.upstream.<name>, and %q does not", path, name)
		case c.Package.Upstream[name] == "":
			return fmt.Errorf("%s: the upstream %q has no version", path, name)
		}
	}
	switch {
	case version == "" && revision == 0:
		return nil
	case version == "":
		return fmt.Errorf("%s: a revision needs a version, and a tracked component has neither", path)
	case revision < 1:
		return fmt.Errorf("%s: a pinned component needs a revision of 1 or more", path)
	case !pinnedVersion.MatchString(version):
		return fmt.Errorf("%s: a version may hold only letters, digits, dots, and underscores, so that the tag <version>-<revision> reads one way, and %q does not", path, version)
	case calendarDate.MatchString(version):
		return fmt.Errorf("%s: the tag %q looks like a release version, and the version of a pinned component is a date in the form YYYYMMDD", path, c.PinnedTag())
	case len(c.Outputs.Images) == 0 || c.Outputs.Deploy != "" || c.Outputs.Channel:
		return fmt.Errorf("%s: a pinned component publishes images, and nothing else", path)
	}
	return nil
}

// validateExclude checks that each excluded path exists in the
// component and is written the way a changed path is written relative
// to the component. A path in any other form matches no changed file,
// so the component would still release on every change to the files
// that it means to exclude.
func (c *Component) validateExclude(path string) error {
	for _, excluded := range c.Outputs.Exclude {
		name := strings.TrimSuffix(excluded, "/")
		if name == "." || !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name {
			return fmt.Errorf("%s: the excluded path %q must be a path inside the component, with no . or .. in it", path, excluded)
		}
		info, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil {
			return fmt.Errorf("%s: the excluded path %q does not exist", path, excluded)
		}
		if info.IsDir() != strings.HasSuffix(excluded, "/") {
			return fmt.Errorf("%s: an excluded directory ends in a slash, and an excluded file does not: %q", path, excluded)
		}
		if deploy := filepath.ToSlash(filepath.Clean(c.Outputs.Deploy)); c.Outputs.Deploy != "" && (within(name, deploy) || within(deploy, name)) {
			return fmt.Errorf("%s: the excluded path %q overlaps the deploy directory %q, and the deploy artifact contains every file in it", path, excluded, c.Outputs.Deploy)
		}
	}
	return nil
}

// validateGraph checks that every dependency is a component and that
// the dependencies have no cycle.
func validateGraph(components map[string]*Component) error {
	for _, c := range sortedComponents(components) {
		for _, dep := range c.Depends.Components {
			other, ok := components[dep]
			if !ok {
				return fmt.Errorf("%s depends on %q, which is not a component", c.Name(), dep)
			}
			// A pinned component's recipe includes the tag of each
			// dependency, and a tracked component has no tag until a
			// release creates one. A dependency that publishes nothing
			// has no tag either, but Recipe hashes what the build reads
			// from it, so a pinned component may depend on it.
			if c.Pinned() && !other.Pinned() && other.HasOutputs() {
				return fmt.Errorf("%s is pinned and depends on %s, which is not, and a pinned component depends only on pinned components", c.Name(), dep)
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

// sortedComponents returns the components ordered by name, so the
// output of this program is the same on every run.
func sortedComponents(components map[string]*Component) []*Component {
	list := make([]*Component, 0, len(components))
	for _, c := range components {
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b *Component) int { return strings.Compare(a.Name(), b.Name()) })
	return list
}
