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

// recipeLabel is the image label that holds a pinned component's
// recipe hash. The plan reads it from the published image and compares
// it with the hash of the tree.
const recipeLabel = "sh.liken.recipe"

// recipeFormat names the way Recipe and the bake file turn a
// component into a build. A change to either one that can change what
// a build makes, such as a new default in the bake template, changes
// this value, so every pinned component needs a new revision.
var recipeFormat = "1"

// Recipe hashes everything a pinned component's images are built from:
// its package.toml, each Dockerfile, every file that Docker sends in
// each build context, the settings of each image's bake target, the
// digest of each image outside the repository that a Dockerfile builds
// from or copies from, the checksum of each file an ADD line fetches,
// and the tag of each component in [depends].
//
// A dependency's tag stands in for its files, because its own recipe
// covers them. So a new revision of a dependency changes the hash of
// every pinned component above it, and each one needs a new revision
// too.
//
// Recipe refuses what it cannot pin. An image outside the repository
// must be named by its digest, because a tag can move to other bytes
// with no change to the tree. A file from the network needs a
// checksum for the same reason. An image of the repository must belong
// to a component in the dependencies, whose tag the hash covers.
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
		// The parser reads a backslash as the continuation character, so
		// a file that changes it could hide a source from the recipe.
		if parsed.Escape {
			return "", fmt.Errorf("%s: %s starts with an escape directive; a pinned component's Dockerfile continues its lines with a backslash", c.Name(), file)
		}
		if err := parsed.SnapshotFirst(); err != nil {
			return "", fmt.Errorf("%s: %s: %w", c.Name(), file, err)
		}
		for _, name := range append(slices.Clone(parsed.From), parsed.Sources...) {
			if _, dir := image.Contexts[name]; dir || name == "scratch" {
				continue
			}
			if owner, ok := producer[name]; ok {
				if !slices.Contains(closure, owner) {
					return "", fmt.Errorf("%s: %s builds on the image %s, and %s is not in the dependencies of %s", c.Name(), file, name, owner, c.Name())
				}
				continue
			}
			if !strings.Contains(name, "@sha256:") {
				return "", fmt.Errorf("%s: %s builds on %s, which names no digest; a pinned component names each image by its digest", c.Name(), file, name)
			}
			lines["image "+name] = true
		}
		for _, remote := range parsed.Remotes {
			if remote.Checksum == "" {
				return "", fmt.Errorf("%s: %s adds %s with no --checksum; a pinned component states the checksum of each file it fetches", c.Name(), file, remote.URL)
			}
			lines["remote "+remote.URL+" "+remote.Checksum] = true
		}
		target, err := newBakeTarget(root, c, image, producer)
		if err != nil {
			return "", err
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
		lines["depends "+dep+" "+components[dep].PinnedTag()] = true
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

// ignoreFile is the ignore file that BuildKit reads for a build context:
// <Dockerfile>.dockerignore beside the Dockerfile when it exists, and
// the context's own .dockerignore when it does not.
func ignoreFile(root, context, dockerfile string) string {
	if _, err := os.Stat(filepath.Join(root, dockerfile+".dockerignore")); err == nil {
		return dockerfile + ".dockerignore"
	}
	return path.Join(context, ".dockerignore")
}

// coveredByDependency is true when a named directory context is the
// build context of an image of another component in the closure, and
// that image reads the context's own .dockerignore. That component's
// recipe then covers exactly the files that Docker sends, and its tag
// is in this recipe. Any other directory can send a file that no
// recipe covers: a subdirectory reads only its own .dockerignore, and
// a <Dockerfile>.dockerignore shapes the dependency's context and not
// this one. So this recipe hashes such a directory itself.
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

// hashContext adds each file of a build context that Docker sends: the
// files under dir, less the ones the ignore file excludes. Each file
// adds whether it is executable, which is the one mode bit that git
// keeps; the other bits follow the umask of the checkout. A symbolic
// link adds its target, the way Docker sends it.
func hashContext(root, dir, ignorePath string, add func(kind, name string, data []byte)) error {
	base := filepath.Join(root, dir)
	var patterns []string
	if f, err := os.Open(filepath.Join(root, ignorePath)); err == nil {
		patterns, err = ignorefile.ReadAll(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", ignorePath, err)
		}
	}
	ignore, err := patternmatcher.New(patterns)
	if err != nil {
		return fmt.Errorf("%s: %w", ignorePath, err)
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

// Recipes hashes every pinned component.
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
