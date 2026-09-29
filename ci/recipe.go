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

// Recipe hashes everything a pinned component's images are built from:
// its package.toml, each Dockerfile, every file that Docker sends in
// each build context, the digest of each image outside the repository
// that a FROM line names, and the tag of each component in [depends].
//
// A dependency's tag stands in for its files, because its own recipe
// covers them. So a new revision of a dependency changes the hash of
// every pinned component above it, and each one needs a new revision
// too.
//
// A FROM line outside the repository must name a digest. A tag can
// move to other bytes with no change to the tree, and the hash would
// not see it.
func Recipe(root string, components map[string]*Component, c *Component) (string, error) {
	producer := ImageProducers(components)
	lines := map[string]bool{}
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
		for _, ref := range parseDockerfile(string(data)).From {
			if _, ok := producer[ref]; ok || ref == "scratch" {
				continue
			}
			if !strings.Contains(ref, "@sha256:") {
				return "", fmt.Errorf("%s: %s starts from %s, which names no digest; a pinned component starts from an image by its digest", c.Name(), file, ref)
			}
			lines["from "+ref] = true
		}
		dirs := []string{path.Join(c.Dir, image.Context)}
		for _, dir := range image.Contexts {
			if owner := ownerOf(components, dir); owner == "" || !slices.Contains(c.Depends.Components, owner) {
				dirs = append(dirs, dir)
			}
		}
		for _, dir := range dirs {
			if err := hashContext(root, dir, add); err != nil {
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

// hashContext adds each file of a build context that Docker sends: the
// files under dir, less the ones its .dockerignore excludes, each with
// its mode. A symbolic link adds its target, the way Docker sends it.
func hashContext(root, dir string, add func(kind, name string, data []byte)) error {
	base := filepath.Join(root, dir)
	var patterns []string
	if f, err := os.Open(filepath.Join(base, ".dockerignore")); err == nil {
		patterns, err = ignorefile.ReadAll(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s/.dockerignore: %w", dir, err)
		}
	}
	ignore, err := patternmatcher.New(patterns)
	if err != nil {
		return fmt.Errorf("%s/.dockerignore: %w", dir, err)
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
		add("file", fmt.Sprintf("%s %o", path.Join(dir, rel), info.Mode().Perm()), data)
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
