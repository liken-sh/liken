package main

import (
	"path"
	"strings"
)

// goCache is the data of the go-restore and go-save templates: the
// go.mod that names the Go version, the files whose hash keys the
// cache, with `go.sum` first, and the family whose entries a restore
// falls back to. Build is true when the job keeps a build cache of its
// own, beside the modules. SavesModules is true when the job saves the
// family's modules entry.
type goCache struct {
	Mod, Family         string
	Keys                []string
	Build, SavesModules bool
}

// HashFiles is the GitHub expression that hashes the key files.
func (g goCache) HashFiles() string {
	return hashFiles(g.Keys)
}

// SumHashFiles is the GitHub expression that hashes `go.sum` alone, the
// key of the modules entry.
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

// jobCache is the Go cache of one job. The repository's Actions cache
// holds 10 GB, and GitHub evicts the oldest entries when the caches
// pass that limit, so the downloaded modules are stored once for each
// module and each `go.sum`, not once for each job and each commit.
//
// Every manual reads the same modules, so the hugo jobs share one
// modules entry, keyed on `go.sum`, and each hugo job saves it when the
// key misses. Any other job restores the modules entry of its own
// module, and only the `go` job saves it: that job runs the module's
// tests, so it reads every module that the other jobs of the module
// read.
//
// Any job other than a hugo job also keeps a build cache of its own.
// Its key holds the hash of go.sum and of the files that choose what
// the job compiles and with which flags: the Makefile where the job
// runs, the component's package.toml, which holds the job's command,
// and its hooks, which a prek job runs. The key holds no commit, so
// main saves an entry only when one of those files or the Go version
// changes. A key with the commit saved an entry of compiled output for
// each job on each push to main, and those entries filled the 10 GB.
// The entry holds every dependency compiled, and a run compiles again
// and tests again only the packages of its module that changed since
// the entry was saved.
func jobCache(c *Component, d jobData) goCache {
	sum := path.Join(d.ModuleDir, "go.sum")
	if d.Toolchain == "hugo" {
		return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Keys: []string{sum}, Family: "manuals", SavesModules: true}
	}
	// A CodeQL job restores the modules entry of the go job of the same
	// module, and keeps no build cache, so the build that CodeQL traces
	// compiles every package of the module and does not take one from an
	// earlier build.
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
