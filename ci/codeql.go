package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// CodeQL reads the repository's own code for faults, and each analysis
// uploads its results to code scanning. GitHub's default setup
// analyzes every language of the whole repository on every push, so
// `liken` derives one CodeQL job for each analysis instead, and the
// plan runs it only when a file that the analysis reads changed, the
// same way it runs any other job.
//
// The loader derives the jobs and appends them to the component's
// jobs. So the plan, the selector, and the workflow see an ordinary
// job, and no package.toml has to repeat its Go and Rust jobs as
// CodeQL jobs. Each derived job is named codeql-<what it analyzes>:
//
//   - A go job gets codeql-<job>, which builds the job's module with
//     `go build ./...` while CodeQL traces the build.
//   - A rust job gets codeql-<job>, which reads the crate in its
//     directory without a build.
//   - A component that holds C or C++ sources gets codeql-c-cpp, and
//     one that holds Python sources gets codeql-python. No job of the
//     repository compiles those files, so the loader finds them by
//     their file names, and each analysis reads them without a build.
//
// The GitHub Actions workflows belong to no component, and the root
// workflow analyzes them in a job of its own: see Planner.Actions.

// codeqlPrefix starts the name of every derived job. A package.toml
// cannot declare a job with it, so a derived name never collides with
// a declared one.
const codeqlPrefix = "codeql-"

// The CodeQL languages that a derived job analyzes.
const (
	languageGo     = "go"
	languageRust   = "rust"
	languageC      = "c-cpp"
	languagePython = "python"
)

// codeqlTimeout is the timeout of a derived job, in minutes. A CodeQL
// analysis of an operator's Go module takes longer than its tests.
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
// declared jobs and from sources, the files of the component, relative
// to the component, that the loader found.
//
// A derived job does not take the apt packages or the environment of
// the job it comes from. Those packages are the programs that the
// job's tests run, such as ffmpeg and cage, and a build reads none of
// them.
func codeqlJobs(c *Component, sources []string) []Job {
	var jobs []Job
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

// codeqlReads is true when the CodeQL job reads the file, given as a
// path relative to the component that owns it. A job reads the sources
// of its language in its own directory. A Go or a Rust analysis also
// reads the sources of its language in each module or crate that the
// job it comes from builds with, because CodeQL follows the data flow
// through them, and jobDeps names those components.
func codeqlReads(job Job, own bool, rest string) bool {
	if !languageSource(job.Language, rest) {
		return false
	}
	return !own || job.Dir == "" || job.Dir == "." || strings.HasPrefix(rest, strings.TrimSuffix(job.Dir, "/")+"/")
}

// codeqlDeps names the components whose sources the CodeQL job reads,
// other than its own: what the job it comes from builds with. A C,
// C++, or Python analysis reads no dependency. nil means every
// component in the closure, as for jobDeps.
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

// codeqlCategory is the category that the job's results upload under.
// Code scanning closes an alert only when a later upload in the same
// category no longer reports it. So each job has a category of its
// own, and the analysis of one component never closes the alerts of
// another, or of another job of the same component.
func codeqlCategory(c *Component, job Job) string {
	return "codeql/" + c.Name() + "/" + strings.TrimPrefix(job.Name, codeqlPrefix)
}

// validateDeclaredName refuses a declared job whose name starts with
// the prefix of the derived jobs.
func validateDeclaredName(path string, job Job) error {
	if strings.HasPrefix(job.Name, codeqlPrefix) {
		return fmt.Errorf("%s: job %q: a job name that starts with %s belongs to the CodeQL jobs that ci/ derives", path, job.Name, codeqlPrefix)
	}
	return nil
}

// githubDir holds the GitHub Actions workflows and the local actions.
const githubDir = ".github/"

// Actions decides whether the run analyzes the GitHub Actions code:
// the workflows and the local actions under .github/. ci/ writes the
// generated workflows from its templates and from each package.toml,
// and the graph job fails a commit whose generated files differ from
// what the generator writes. So every change to the Actions code is a
// change to a file under .github/, and a change to a template or a
// package.toml that writes no different workflow changes no Actions
// code. The analysis runs when a file under .github/ changed, and when
// every job runs.
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
