package main

import (
	"path"
	"strings"
)

// goCache is the data of the go-restore and go-save templates. Mod is
// the go.mod that sets the Go version. Keys are the files whose hash is
// the cache key, with go.sum first. A restore falls back to any entry of
// the same Family. Build is true when the job has a build cache besides
// the modules. SavesModules is true when the job saves the family's
// modules entry.
type goCache struct {
	Mod, Family         string
	Keys                []string
	Build, SavesModules bool
}

func (g goCache) HashFiles() string {
	return hashFiles(g.Keys)
}

// SumHashFiles is the GitHub expression that hashes go.sum alone, which
// is the key of the modules entry.
func (g goCache) SumHashFiles() string {
	return hashFiles(g.Keys[:1])
}

func hashFiles(files []string) string {
	quoted := make([]string, len(files))
	for i, file := range files {
		quoted[i] = "'" + file + "'"
	}
	return "hashFiles(" + strings.Join(quoted, ", ") + ")"
}

// jobCache returns the Go cache of one job. A repository's Actions cache
// holds 10 GB by default, and GitHub evicts the least recently used
// entries when the caches pass that limit. So the downloaded modules are
// saved once for each go.sum, not once for each job.
//
// Every manual reads the same modules, so the hugo jobs share one
// modules entry, and each hugo job saves it when the key misses. Any
// other job restores the modules entry of its own module, and only the
// go job saves it, because that job runs the module's tests and so
// downloads every module that the module's other jobs read.
//
// Each job except a hugo or CodeQL job also has a build cache of its
// own. Its key hashes go.sum and the files that decide what the job
// compiles and with which flags: the Makefile where the job runs, the
// component's package.toml, which sets the job's command, and the
// component's .pre-commit-config.yaml, whose hooks a prek job runs. The
// key does not include the commit, because then main would save a new
// entry on each push and fill the cache. So main saves an entry only
// when one of those files or the Go version changes, and a run restores
// it and recompiles only the packages that changed since.
func jobCache(c *Component, d jobData) goCache {
	sum := path.Join(d.ModuleDir, "go.sum")
	if d.Toolchain == "hugo" {
		return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Keys: []string{sum}, Family: "manuals", SavesModules: true}
	}
	// CodeQL traces the compiler, so a CodeQL job must compile every
	// package in the run and has no build cache.
	if d.Toolchain == "codeql" {
		return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Keys: []string{sum}, Family: d.ModuleDir}
	}
	return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Family: d.ModuleDir, Build: true,
		SavesModules: d.Toolchain == "go", Keys: []string{
			sum,
			path.Join(d.WorkDir, "Makefile"),
			path.Join(c.Dir, "package.toml"),
			path.Join(c.Dir, ".pre-commit-config.yaml"),
		}}
}
