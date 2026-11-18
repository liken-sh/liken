package main

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// A job runs only when a changed file is one that its toolchain reads,
// because a change to any other file cannot change the job's result.
// In a component with a manual, the manual is the docs/ directory and
// the skills/ directory that the guides generate. brand has no manual,
// and its skills/ is the Go program that generates every manual's
// skills.
//
// Of the files of its own component:
//
//   - A Go job reads every file except Rust sources, the manual, and
//     the smoke checks. It reads every Go file, also in docs/ and
//     smoke/, because gofmt walks every directory. It reads data
//     files, because a Go test can open any file by path.
//   - A Rust job reads every file except Go sources, the manual, and
//     the smoke checks.
//   - A hugo job reads every file except Rust sources, because a manual
//     can run the component's Go code to build, as one that generates
//     its API reference with go run does.
//   - The OS build reads every file except the manual, which is not in
//     the OS image.
//   - A CodeQL job reads only the sources of its language in its own
//     directory, because the analysis reports results only for them.
//   - Every other job, such as prek, reads every file.
//
// Of the files of the components in its closure, except files that no
// output is built from (see isNotOutput):
//
//   - A Go job reads every file except Rust sources of the components
//     that its go.mod replaces with a local directory. Go applies only the main module's replace
//     directives, and the deps check fails when a module of this
//     repository would come from the proxy, so the job's go.mod lists
//     every module of the repository that the job compiles.
//   - A Rust job reads the components that hold the crates its crate
//     depends on by path, followed to the end of the chain. It reads
//     every file of those components except Go sources, because a crate
//     can include a file beside it, as brand's crate includes its
//     stylesheet.
//   - A hugo job reads the components that the go.mod of its manual
//     and the go.mod of its component replace. It also reads every file
//     of brand except Rust sources, for the theme, even when brand is
//     not in the component's [depends].
//   - A Go or Rust CodeQL job reads the sources of its language in the
//     components that its go.mod replaces or that its crate depends on
//     by path. A C, C++, or Python CodeQL job reads none.
//   - Every other job, such as prek and the OS build, reads every
//     component. A prek hook such as go mod tidy reads the modules that
//     go.mod replaces.
//   - No job reads a pinned component, such as a base image. A change
//     to it builds the images that build on it (see imageReads).
//
// A job also reads each file that the reads list of the job in
// package.toml lists, such as a file of test cases that a Go test and a
// Rust test share, because no toolchain finds such a file through a
// dependency. When a go.mod or a Cargo.toml cannot be parsed, the job
// reads every component, so it runs and fails on that file.

// rustSource is true for a file that only Rust builds read.
func rustSource(file string) bool {
	switch path.Base(file) {
	case "Cargo.toml", "Cargo.lock", "rust-toolchain.toml":
		return true
	}
	return path.Ext(file) == ".rs"
}

// goSource is true for a file that only Go builds read.
func goSource(file string) bool {
	switch path.Base(file) {
	case "go.mod", "go.sum":
		return true
	}
	return path.Ext(file) == ".go"
}

// ownReads is true when the job reads a file of its own component.
// rest is the file's path relative to the component.
func ownReads(c *Component, job Job, rest string) bool {
	manual := c.Docs != nil && (strings.HasPrefix(rest, "docs/") || strings.HasPrefix(rest, "skills/"))
	aside := manual || strings.HasPrefix(rest, "smoke/")
	switch job.Toolchain {
	case "go":
		return !rustSource(rest) && (goSource(rest) || !aside)
	case "rust":
		return !goSource(rest) && !aside
	case "hugo":
		return !rustSource(rest)
	case "os":
		return !manual
	case "codeql":
		return codeqlReads(job, true, rest)
	}
	return true
}

// jobDeps returns the other components whose files the job reads
// through its toolchain. A nil map means every component in the
// closure.
func (s selector) jobDeps(c *Component, job Job) map[string]bool {
	var deps map[string]bool
	var err error
	switch job.Toolchain {
	case "codeql":
		return s.codeqlDeps(c, job)
	case "go":
		deps, err = s.goReplaces(path.Join(c.Dir, orDefault(job.Module, job.Dir)))
	case "rust":
		deps, err = s.cratePaths(path.Join(c.Dir, job.Dir))
	case "hugo":
		var root map[string]bool
		if root, err = s.goReplaces(c.Dir); err == nil {
			if deps, err = s.goReplaces(path.Join(c.Dir, orDefault(job.Module, job.Dir))); err == nil {
				maps.Copy(deps, root)
			}
		}
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	return deps
}

// jobReads is true when the job reads the changed file. deps is the
// result of jobDeps for the job.
func (s selector) jobReads(c *Component, job Job, deps map[string]bool, file string) bool {
	if slices.Contains(job.Reads, file) {
		return true
	}
	owner := s.components[ownerOf(s.components, file)]
	if owner == nil {
		return false
	}
	rest := strings.TrimPrefix(file, owner.Dir+"/")
	if strings.HasPrefix(rest, plansDir) {
		return false
	}
	if owner.Name() == c.Name() {
		return ownReads(c, job, rest)
	}
	if job.Toolchain == "hugo" && owner.Name() == "brand" {
		return !rustSource(rest)
	}
	if !slices.Contains(Closure(s.components, c.Name()), owner.Name()) || owner.isNotOutput(rest) || owner.Pinned() {
		return false
	}
	if deps != nil && !deps[owner.Name()] {
		return false
	}
	switch job.Toolchain {
	case "go", "hugo":
		return !rustSource(rest)
	case "rust":
		return !goSource(rest)
	case "codeql":
		return codeqlReads(job, false, rest)
	}
	return true
}

// goReplaces returns the components that hold the local directories
// in the replace directives of the go.mod in dir. A directory with no
// go.mod returns an empty map.
func (s selector) goReplaces(dir string) (map[string]bool, error) {
	deps := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(s.root, dir, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return deps, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		_, target, ok := strings.Cut(line, "=>")
		fields := strings.Fields(target)
		if !ok || len(fields) == 0 {
			continue
		}
		if local := fields[0]; local == "." || local == ".." || strings.HasPrefix(local, "./") || strings.HasPrefix(local, "../") {
			if owner := ownerOf(s.components, path.Join(dir, local)); owner != "" {
				deps[owner] = true
			}
		}
	}
	return deps, nil
}

// cratePaths returns the components that hold the crate in dir and
// every crate that it or its workspace members depend on by path,
// followed to the end of the chain. A directory with no Cargo.toml
// returns an empty map.
func (s selector) cratePaths(dir string) (map[string]bool, error) {
	deps := map[string]bool{}
	seen := map[string]bool{}
	var visit func(dir string) error
	visit = func(dir string) error {
		if seen[dir] {
			return nil
		}
		seen[dir] = true
		var manifest map[string]any
		_, err := toml.DecodeFile(filepath.Join(s.root, dir, "Cargo.toml"), &manifest)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if owner := ownerOf(s.components, dir); owner != "" {
			deps[owner] = true
		}
		var next []string
		for _, p := range dependencyPaths(manifest, false) {
			next = append(next, path.Join(dir, p))
		}
		if workspace, ok := manifest["workspace"].(map[string]any); ok {
			members, _ := workspace["members"].([]any)
			for _, member := range members {
				pattern, _ := member.(string)
				matches, err := filepath.Glob(filepath.Join(s.root, dir, pattern))
				if err != nil {
					return err
				}
				for _, match := range matches {
					rel, err := filepath.Rel(s.root, match)
					if err != nil {
						return err
					}
					next = append(next, filepath.ToSlash(rel))
				}
			}
		}
		for _, n := range next {
			if err := visit(n); err != nil {
				return err
			}
		}
		return nil
	}
	return deps, visit(dir)
}

// dependencyPaths returns the path of every path dependency in a parsed
// Cargo manifest, from any table whose name ends in "dependencies" and
// from [patch]. It skips the path of a [lib] or [[bin]] table, which is
// a source file of the crate itself. inDeps is true inside a dependency
// table.
func dependencyPaths(table map[string]any, inDeps bool) []string {
	var paths []string
	for key, value := range table {
		switch v := value.(type) {
		case map[string]any:
			paths = append(paths, dependencyPaths(v, inDeps || strings.HasSuffix(key, "dependencies") || key == "patch")...)
		case string:
			if inDeps && key == "path" {
				paths = append(paths, v)
			}
		}
	}
	return paths
}

// readsVersion is true when the image's Dockerfile, or the Dockerfile
// of an unpinned image of the repository that it builds on, declares
// the VERSION build argument. It is false for a pinned image, because
// its VERSION is the pinned version and does not change between
// publishes. seen stops a cycle of base images. A Dockerfile that does
// not parse returns true.
func (s selector) readsVersion(c *Component, image Image, seen map[string]bool) bool {
	if c.Pinned() || seen[image.Name] {
		return false
	}
	seen[image.Name] = true
	parsed, err := readDockerfile(filepath.Join(s.root, c.Dir, orDefault(image.File, "Dockerfile")))
	if err != nil {
		return true
	}
	for _, arg := range parsed.Args {
		if arg == "VERSION" {
			return true
		}
	}
	for _, base := range parsed.Bases(image, s.producer) {
		owner := s.components[s.producer[base]]
		for _, other := range owner.Outputs.Images {
			if other.Name == base && s.readsVersion(owner, other, seen) {
				return true
			}
		}
	}
	return false
}
