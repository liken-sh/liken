package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// recipeLabel is the image label whose value is a pinned component's
// recipe. `ci bases` and `ci plan` read it from the published image and
// compare it with the recipe of the tree.
const recipeLabel = "sh.liken.recipe"

// recipeFormat is part of every recipe. Raise it when Recipe or the
// bake template changes what a build makes, or when the publish changes
// the labels it writes on a pinned image. The new value changes the
// recipe of every pinned component, so each one needs a new revision.
//
// A label change needs the new revision because a published tag is
// never overwritten. Without it, the old tag keeps the old labels, and
// `ci bases` still reads its recipe as current. pinnedLabelsOfFormat in
// imagemeta_test.go lists the labels of each format, and its test fails
// when the labels change and this value does not.
var recipeFormat = "2"

// Recipe returns a hash of everything a pinned component's images are
// built from: its package.toml, each Dockerfile, every file that
// Docker sends in each build context, the settings of each image's
// bake target, the digest of each outside image that a Dockerfile
// builds from or copies from, the checksum of each file that an ADD
// line fetches, and the tag of each component in [depends].
//
// For a dependency that publishes, the hash covers its tag and not its
// files, because the dependency's own recipe covers its files. So a new
// revision of a dependency changes the recipe of each pinned component
// that depends on it, and each of those needs a new revision too.
//
// A dependency that publishes nothing, such as toolchains, has no tag.
// For it, the recipe covers what the build reads: the reference and
// digest that toolchains/images.toml gives each bare name that a
// Dockerfile uses. Entries that no Dockerfile of the component uses are
// left out.
//
// Recipe returns an error for anything it cannot pin. An outside image
// must be named by digest, and a file fetched from the network must
// have a checksum, because a tag or a URL can serve other bytes with no
// change to the tree. An image of the repository must belong to a
// component in the dependencies, because the hash covers that
// component's tag.
func Recipe(root string, components map[string]*Component, c *Component) (string, error) {
	producer := ImageProducers(components)
	closure := Closure(components, c.Name())
	lines := map[string]bool{"format " + recipeFormat: true}
	add := func(kind, name string, data []byte) {
		sum := sha256.Sum256(data)
		lines[kind+" "+name+" "+hex.EncodeToString(sum[:])] = true
	}
	manifest, err := os.ReadFile(filepath.Join(root, c.Dir, "package.toml"))
	if err != nil {
		return "", err
	}
	add("package", path.Join(c.Dir, "package.toml"), manifest)
	for _, image := range c.Outputs.Images {
		file := path.Join(c.Dir, orDefault(image.File, "Dockerfile"))
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return "", err
		}
		add("dockerfile", file, data)
		parsed := parseDockerfile(string(data))
		// parseDockerfile reads a backslash as the line continuation.
		// An escape directive changes that character, and the parser
		// would then miss sources that the recipe must hash.
		if parsed.Escape {
			return "", fmt.Errorf("%s: %s starts with an escape directive, and a pinned component's Dockerfile must continue its lines with a backslash", c.Name(), file)
		}
		if err := parsed.SnapshotFirst(); err != nil {
			return "", fmt.Errorf("%s: %s: %w", c.Name(), file, err)
		}
		target, err := newBakeTarget(root, c, image, producer, components)
		if err != nil {
			return "", err
		}
		resolved := map[string]string{}
		for _, context := range target.Contexts {
			if reference, ok := strings.CutPrefix(context.Value, "docker-image://"); ok {
				resolved[context.Name] = reference
			}
		}
		for _, name := range append(slices.Clone(parsed.From), parsed.Sources...) {
			if _, dir := image.Contexts[name]; dir || name == "scratch" {
				continue
			}
			if reference, ok := resolved[name]; ok {
				lines["image "+reference] = true
				continue
			}
			if owner, ok := producer[name]; ok {
				if !slices.Contains(closure, owner) {
					return "", fmt.Errorf("%s: %s builds on the image %s, and %s is not in the dependencies of %s", c.Name(), file, name, owner, c.Name())
				}
				continue
			}
			if !strings.Contains(name, "@sha256:") {
				return "", fmt.Errorf("%s: %s builds on %s, which has no digest, and a pinned component must name each image by its digest", c.Name(), file, name)
			}
			lines["image "+name] = true
		}
		for _, remote := range parsed.Remotes {
			if remote.Checksum == "" {
				return "", fmt.Errorf("%s: %s adds %s with no --checksum, and a pinned component must give a checksum for each file it fetches", c.Name(), file, remote.URL)
			}
			lines["remote "+remote.URL+" "+remote.Checksum] = true
		}
		lines["bake "+target.settings()] = true
		context := path.Join(c.Dir, image.Context)
		if err := hashContext(root, context, ignoreFile(root, context, file), add); err != nil {
			return "", fmt.Errorf("%s: %w", c.Name(), err)
		}
		for _, dir := range image.Contexts {
			if coveredByDependency(root, components, closure, c.Name(), dir) {
				continue
			}
			if err := hashContext(root, dir, path.Join(dir, ".dockerignore"), add); err != nil {
				return "", fmt.Errorf("%s: %w", c.Name(), err)
			}
		}
	}
	for _, dep := range c.Depends.Components {
		if components[dep].HasOutputs() {
			lines["depends "+dep+" "+components[dep].PinnedTag()] = true
		}
	}
	sorted := slices.Sorted(func(yield func(string) bool) {
		for line := range lines {
			if !yield(line) {
				return
			}
		}
	})
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n") + "\n"))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ignoreFile returns the path of the ignore file that BuildKit reads
// for a build context: <Dockerfile>.dockerignore beside the Dockerfile
// when it exists, and the context's own .dockerignore otherwise.
func ignoreFile(root, context, dockerfile string) string {
	if _, err := os.Stat(filepath.Join(root, dockerfile+".dockerignore")); err == nil {
		return dockerfile + ".dockerignore"
	}
	return path.Join(context, ".dockerignore")
}

// coveredByDependency reports whether a named directory context is the
// build context of an image of another component in the closure, and
// that image reads the context's own .dockerignore. Then the
// dependency's recipe already hashes the same files that Docker sends
// for this context, so this recipe skips them.
//
// Any other directory can send files that no other recipe hashes. A
// subdirectory of a dependency's context reads only its own
// .dockerignore, and a <Dockerfile>.dockerignore filters only the
// dependency's own build.
func coveredByDependency(root string, components map[string]*Component, closure []string, self, dir string) bool {
	for _, name := range closure {
		if name == self {
			continue
		}
		dep := components[name]
		for _, image := range dep.Outputs.Images {
			context := path.Join(dep.Dir, image.Context)
			file := path.Join(dep.Dir, orDefault(image.File, "Dockerfile"))
			if dir == context && ignoreFile(root, context, file) == path.Join(context, ".dockerignore") {
				return true
			}
		}
	}
	return false
}

// hashContext calls add for each file of a build context that Docker
// sends: the files under dir that the ignore file does not exclude.
// The name includes the file's mode, either 644, 755, or link, because
// git records only whether a file is executable, and the other
// permission bits follow the umask of the checkout. For a symbolic
// link, the data is its target, because Docker sends the link itself.
func hashContext(root, dir, ignorePath string, add func(kind, name string, data []byte)) error {
	base := filepath.Join(root, dir)
	ignore, err := readIgnore(root, ignorePath)
	if err != nil {
		return err
	}
	return filepath.WalkDir(base, func(file string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, file)
		rel = filepath.ToSlash(rel)
		if skip, err := ignore.MatchesOrParentMatches(rel); err != nil || skip {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		var data []byte
		if e.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(file)
			if err != nil {
				return err
			}
			data = []byte(target)
		} else if data, err = os.ReadFile(file); err != nil {
			return err
		}
		mode := "644"
		if e.Type()&fs.ModeSymlink != 0 {
			mode = "link"
		} else if info.Mode().Perm()&0o111 != 0 {
			mode = "755"
		}
		add("file", path.Join(dir, rel)+" "+mode, data)
		return nil
	})
}

// readIgnore parses a build context's ignore file. A missing file
// excludes nothing.
func readIgnore(root, ignorePath string) (*patternmatcher.PatternMatcher, error) {
	var patterns []string
	if f, err := os.Open(filepath.Join(root, ignorePath)); err == nil {
		patterns, err = ignorefile.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", ignorePath, err)
		}
	}
	ignore, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, fmt.Errorf("parsing the patterns of %s: %w", ignorePath, err)
	}
	return ignore, nil
}

func Recipes(root string, components map[string]*Component) (map[string]string, error) {
	recipes := map[string]string{}
	for _, c := range sortedComponents(components) {
		if !c.Pinned() {
			continue
		}
		recipe, err := Recipe(root, components, c)
		if err != nil {
			return nil, err
		}
		recipes[c.Name()] = recipe
	}
	return recipes, nil
}
