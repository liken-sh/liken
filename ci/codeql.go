package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// GitHub's default CodeQL setup analyzes every language of the whole
// repository on every push. So the loader derives a CodeQL job for each
// analysis of a component and adds it to the component's jobs, and the
// plan runs it only when a file that the analysis reads changed. Each
// job uploads its results to code scanning:
//
//   - A go job gets codeql-<job>, which runs `go build ./...` in the
//     job's module while CodeQL traces the build.
//   - A rust job gets codeql-<job>, which analyzes the crate in its
//     directory without a build.
//   - A component with C or C++ sources gets codeql-c-cpp, and one with
//     Python sources gets codeql-python. No job of the repository
//     compiles those files, so the loader finds them by their file
//     extensions, and the analysis runs without a build.
//
// The workflows under .github/ belong to no component, so the root
// workflow analyzes them in a job of its own (see Planner.Actions).

// codeqlPrefix starts the name of every derived job.
// validateDeclaredName refuses a declared job with this prefix, so a
// derived name never collides with a declared one.
const codeqlPrefix = "codeql-"

// The CodeQL languages that a derived job analyzes.
const (
	languageGo     = "go"
	languageRust   = "rust"
	languageC      = "c-cpp"
	languagePython = "python"
)

// codeqlTimeout is the timeout of a derived job, in minutes. A CodeQL
// analysis of an operator's Go module takes longer than the 15 minutes
// that a job gets by default.
const codeqlTimeout = 30

// languageSource is true for a file that the analysis of the language
// reads. A Go analysis compiles the module, so it reads go.mod and
// go.sum too, and a Rust analysis reads the crate's manifests.
func languageSource(language, file string) bool {
	switch language {
	case languageGo:
		return goSource(file)
	case languageRust:
		return rustSource(file)
	case languageC:
		switch path.Ext(file) {
		case ".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx":
			return true
		}
	case languagePython:
		return path.Ext(file) == ".py"
	}
	return false
}

// codeqlJobs derives the CodeQL jobs of the component from its
// declared jobs and from sources, the paths of the component's files
// relative to the component.
//
// A derived job does not copy the apt packages or the env of its
// declared job, because those packages are programs that the tests run,
// such as ffmpeg, and the build does not use them. Two jobs of one
// language in one directory, such as a module's tests and its lint,
// build the same code, so they derive one job, named after the first.
func codeqlJobs(c *Component, sources []string) []Job {
	var jobs []Job
	analyzed := map[string]bool{}
	for _, job := range c.Jobs {
		language := ""
		switch job.Toolchain {
		case "go":
			language = languageGo
		case "rust":
			language = languageRust
		default:
			continue
		}
		dir := job.Dir
		if language == languageGo {
			dir = orDefault(job.Module, job.Dir)
		}
		if analyzed[language+" "+dir] {
			continue
		}
		analyzed[language+" "+dir] = true
		derived := Job{Name: codeqlPrefix + job.Name, Toolchain: "codeql", Language: language, Dir: dir, Timeout: codeqlTimeout}
		if language == languageGo {
			derived.Run = "go build ./..."
		}
		jobs = append(jobs, derived)
	}
	for _, language := range []string{languageC, languagePython} {
		if slices.ContainsFunc(sources, func(file string) bool { return languageSource(language, file) }) {
			jobs = append(jobs, Job{Name: codeqlPrefix + language, Toolchain: "codeql", Language: language, Timeout: codeqlTimeout})
		}
	}
	return jobs
}

// codeqlReads is true when the CodeQL job analyzes the file. rest is
// the file's path relative to the component that holds it, and own is
// true when that is the job's component. In its own component the job
// reads the sources of its language under its directory. In another
// component it reads every source of its language, because a Go or Rust
// analysis follows data flow into the modules and crates that the build
// uses. codeqlDeps finds those components.
func codeqlReads(job Job, own bool, rest string) bool {
	if !languageSource(job.Language, rest) {
		return false
	}
	return !own || job.Dir == "" || job.Dir == "." || strings.HasPrefix(rest, strings.TrimSuffix(job.Dir, "/")+"/")
}

// codeqlDeps returns the other components whose sources the CodeQL job
// reads: those that hold a module that go.mod replaces with a local
// directory, or a crate that the job's crate depends on by path. A C,
// C++, or Python analysis reads none. A nil map means every component
// in the closure, as for jobDeps.
func (s selector) codeqlDeps(c *Component, job Job) map[string]bool {
	var deps map[string]bool
	var err error
	switch job.Language {
	case languageGo:
		deps, err = s.goReplaces(path.Join(c.Dir, job.Dir))
	case languageRust:
		deps, err = s.cratePaths(path.Join(c.Dir, job.Dir))
	default:
		return map[string]bool{}
	}
	if err != nil {
		return nil
	}
	return deps
}

// codeqlCategory is the code scanning category of the job's upload.
// Code scanning closes an alert when a later upload in the same
// category does not report it, so each job uploads under its own
// category and never closes the alerts of another job.
func codeqlCategory(c *Component, job Job) string {
	return "codeql/" + c.Name() + "/" + strings.TrimPrefix(job.Name, codeqlPrefix)
}

func validateDeclaredName(path string, job Job) error {
	if strings.HasPrefix(job.Name, codeqlPrefix) {
		return fmt.Errorf("%s: job %q: the name starts with %s, which is reserved for the CodeQL jobs that ci/ derives", path, job.Name, codeqlPrefix)
	}
	return nil
}

// githubDir holds the GitHub Actions workflows and the local actions.
const githubDir = ".github/"

// Actions reports whether the run analyzes the GitHub Actions code
// under .github/, with the reason. It runs when a file under .github/
// changed or when every job runs. A change to a template or a
// package.toml needs no check of its own, because the graph job fails a
// commit whose generated workflows differ from the generator's output,
// so every change to the workflows also changes a file under .github/.
func (p Planner) Actions(e Event) (bool, string, error) {
	list, all := p.Comparisons(e)
	if all != "" {
		return true, "every job runs", nil
	}
	diff, err := p.readDiffs(list, e.Head)
	if err != nil {
		return false, "", err
	}
	if diff.GitHub != "" {
		return true, "changed: " + diff.GitHub, nil
	}
	return false, "no file under " + githubDir + " changed", nil
}
