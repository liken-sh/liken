package main

import (
	"fmt"
	"slices"
	"strings"
)

// A Job is one check of a component, run as one job in the component's
// workflow.
type Job struct {
	Name string `toml:"name"`
	// Toolchain selects the setup steps that run before the command.
	Toolchain string `toml:"toolchain"`
	// Dir is the job's working directory, relative to the component.
	Dir string `toml:"dir"`
	// Module is the directory of the go.mod that a go or hugo job reads
	// its Go version from, relative to the component. It defaults to
	// Dir.
	Module string `toml:"module"`
	// Run is the command, usually a make target that works on a
	// workstation too. Each line is a separate command.
	Run string `toml:"run"`
	// Timeout is in minutes.
	Timeout int `toml:"timeout"`
	// Apt lists the Ubuntu packages to install on the runner before the
	// command.
	Apt []string          `toml:"apt"`
	Env map[string]string `toml:"env"`
	// Coverage lists the files, relative to the component, that the job
	// writes for the site's coverage report.
	Coverage []string `toml:"coverage"`
	// Artifacts lists files, relative to the component, that the job
	// uploads whether it passes or fails, such as a boot log.
	Artifacts []string `toml:"artifacts"`
	// Skip lists the hooks that a prek job does not run, because
	// another job of the component already runs the same command.
	Skip []string `toml:"skip"`
	// Reads lists files of other components, relative to the root of the
	// repository, that the job uses although its toolchain does not
	// detect them, such as a file of test cases that the tests of two
	// components share. A change to one of them runs the job. It does
	// not release the component, because no output is built from it.
	Reads []string `toml:"reads"`
	// Shards splits a slow test run across parallel jobs. The workflow
	// runs ShardRun in Shards jobs, each with SHARD set to its number,
	// counting from 1, and SHARDS to the total, and each uploads its
	// coverage files. The job itself then downloads those files into
	// coverage-shards/ under the component and runs Run, which merges
	// them and checks the result. Zero and one both mean a single job.
	Shards   int    `toml:"shards"`
	ShardRun string `toml:"shard-run"`
	// Language is the CodeQL language of a codeql job. codeql.go
	// generates those jobs, so the field has no TOML key.
	Language string `toml:"-"`
	// Grype is true for a job whose command scans its build output with
	// grype and writes the SARIF to grypeSARIF. The workflow uploads that
	// file to code scanning under the category grype/<component>.
	Grype bool `toml:"grype"`
}

// toolchains lists the values of Job.Toolchain that the workflow
// template has setup steps for.
var toolchains = []string{"go", "hugo", "rust", "prek", "os"}

// grypeSARIF is the file that a job with grype = true must write its
// scan to, because the upload step reads only this path.
func grypeSARIF(c *Component) string { return "$GITHUB_WORKSPACE/grype/" + c.Name() + ".sarif" }

func (c *Component) validateJob(path string, job Job) error {
	if job.Name == "" || (job.Run == "") != (job.Toolchain == "prek") {
		return fmt.Errorf("%s: a job needs a name, and a job needs a run command unless its toolchain is prek, which takes none", path)
	}
	if err := validateDeclaredName(path, job); err != nil {
		return err
	}
	if !slices.Contains(toolchains, job.Toolchain) {
		return fmt.Errorf("%s: job %q: the toolchain %q is not one of %s", path, job.Name, job.Toolchain, strings.Join(toolchains, ", "))
	}
	if len(job.Skip) > 0 && job.Toolchain != "prek" {
		return fmt.Errorf("%s: job %q: only a prek job skips hooks", path, job.Name)
	}
	if (job.Shards > 1) != (job.ShardRun != "") {
		return fmt.Errorf("%s: job %q: shards above one need a shard-run command, and a shard-run command needs shards above one", path, job.Name)
	}
	if job.Shards > 1 && len(job.Coverage) == 0 {
		return fmt.Errorf("%s: job %q: a sharded job needs coverage files, because the job downloads the coverage that each shard uploads", path, job.Name)
	}
	if job.Grype && !strings.Contains(job.Run, grypeSARIF(c)) {
		return fmt.Errorf("%s: job %q: a job with grype = true writes its scan to %s, and its run command does not contain that path", path, job.Name, grypeSARIF(c))
	}
	if !job.Grype && strings.Contains(job.Run, grypeSARIF(c)) {
		return fmt.Errorf("%s: job %q: the run command writes a grype scan that no step uploads, so set grype = true", path, job.Name)
	}
	return nil
}
