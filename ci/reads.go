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

// A job reads the files that its toolchain builds and tests from, and
// a change that none of them reads cannot change the job's result. The
// rules below describe the jobs of this repository, and each rule
// follows from how the toolchain finds its files:
//
//   - A Go job reads every Go file of its component, because its gofmt
//     check walks every directory, and every other file except the Rust
//     sources, the manual, and the smoke checks. A Go test can read
//     any data file by its path, such as a file of cases that a Rust
//     crate reads too, so a data file runs it.
//   - A Rust job reads every file of its component except the Go
//     sources, the manual, and the smoke checks.
//   - A manual's hugo job reads every file of its component except the
//     Rust sources: its build can run the component's Go code, as a
//     manual does that generates its API reference with go run.
//   - The OS build reads every file of the OS except the manual, which
//     is not an OS artifact and ships to the site on its own path.
//   - A prek job reads every file, because its hooks check every file
//     of the component.
//
// The manual is docs/ and the skills/ that its guides generate, in a
// component that has a manual. brand has no manual, and its skills/
// is the Go program that generates every manual's skills.
//
// A job reads a dependency only through what its toolchain builds
// from. A Go job reads the Go modules that its go.mod replaces with a
// directory: Go honors the replace directives of the main module
// alone, and the deps check fails a module of the repository that Go
// would fetch from the proxy, so those directives name every module of
// the repository that the job compiles. A Rust job reads the crates
// that its crate takes by path, through every crate they take by path
// in turn, and every other file of the components that hold them,
// because a crate can include a file beside it, as brand's crate
// includes the stylesheet. A hugo job reads what its manual's go.mod
// and its component's go.mod replace, and brand's theme. A prek job and
// the OS build read every dependency, because a hook such as go mod
// tidy reads the modules that go.mod replaces. A pinned dependency is a
// base image, and only images build on it.
//
// A go.mod or a Cargo.toml that cannot be read makes the job read every
// dependency, so the job runs and fails there.

// rustSource is true for a file that only a Rust build reads.
func rustSource(file string) bool {
	switch path.Base(file) {
	case "Cargo.toml", "Cargo.lock", "rust-toolchain.toml":
		return true
	}
	return path.Ext(file) == ".rs"
}

// goSource is true for a file that only a Go build reads.
func goSource(file string) bool {
	switch path.Base(file) {
	case "go.mod", "go.sum":
		return true
	}
	return path.Ext(file) == ".go"
}

// ownReads is true when the job reads the file of its own component,
// given as a path relative to the component.
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
	}
	return true
}

// jobDeps names the components whose files the job reads through its
// toolchain, other than its own. nil means every component in the
// closure.
func (s selector) jobDeps(c *Component, job Job) map[string]bool {
	var deps map[string]bool
	var err error
	switch job.Toolchain {
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

// jobReads is true when the job reads the changed file. deps is what
// jobDeps returns for the job.
func (s selector) jobReads(c *Component, job Job, deps map[string]bool, file string) bool {
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
	// Every manual builds with brand's theme, whether or not the
	// component names brand in [depends].
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
	}
	return true
}

// goReplaces names the components that the go.mod in dir replaces a
// module with. A directory with no go.mod replaces nothing.
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

// cratePaths names the components that hold the crate in dir, and every
// crate that it or its workspace members take by path, through the
// whole chain. A directory with no Cargo.toml takes nothing.
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

// dependencyPaths lists the path of every dependency in a Cargo
// manifest: under [dependencies], [dev-dependencies],
// [build-dependencies], their [target] and [workspace] forms, and
// [patch]. The path of a [lib] or a [[bin]] names a source file of the
// crate itself, and it is not a dependency.
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

// readsVersion is true when the image's build reads the VERSION build
// argument, directly or through a tracked image of the repository that
// it builds on. A pinned image's VERSION is its own pinned version, so
// it never changes from one publish to the next.
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
