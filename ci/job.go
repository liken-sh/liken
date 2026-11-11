package main

import (
	"fmt"
	"slices"
	"strings"
)

// A Job is one check that the gate runs for the component, as one job
// in the component's workflow.
type Job struct {
	Name string `toml:"name"`
	// Toolchain names the setup that the job needs before its command.
	// The workflow template has one block of setup steps for each.
	Toolchain string `toml:"toolchain"`
	// Dir is the job's working directory, relative to the component.
	Dir string `toml:"dir"`
	// Module is the directory of the go.mod that a go or hugo job
	// reads its Go version from, relative to the component. It
	// defaults to Dir.
	Module string `toml:"module"`
	// Run is the command, usually a make target that a workstation
	// runs too. Each line is one command.
	Run string `toml:"run"`
	// Timeout is in minutes.
	Timeout int `toml:"timeout"`
	// Apt names the Ubuntu packages the command needs on the runner.
	Apt []string          `toml:"apt"`
	Env map[string]string `toml:"env"`
	// Coverage names the files, relative to the component, that the
	// job writes for the site's coverage report.
	Coverage []string `toml:"coverage"`
	// Artifacts names files, relative to the component, that the job
	// uploads whether it passes or fails, such as a boot log.
	Artifacts []string `toml:"artifacts"`
	// Skip names the hooks that a prek job leaves out, because another
	// job of the component runs the same command.
	Skip []string `toml:"skip"`
	// Reads names files of other components, relative to the root of
	// the repository, that the job reads although its toolchain does not
	// find them, such as a file of test cases that two components'
	// tests share. A change to one runs the job. It does not release the
	// component, because no output is built from it.
	Reads []string `toml:"reads"`
	// Shards splits a job whose tests take more processor time than
	// one runner gives in a few minutes. The workflow runs ShardRun in
	// Shards jobs at once, with SHARD set to the number of each, from
	// 1, and SHARDS to the count, and each uploads its coverage files.
	// Then the job itself downloads them into coverage-shards/ under
	// the component and runs Run, which merges them and checks the
	// result. Zero and one mean one job.
	Shards   int    `toml:"shards"`
	ShardRun string `toml:"shard-run"`
	// Language is the CodeQL language of a job whose toolchain is
	// codeql. No package.toml declares such a job, because codeql.go
	// derives each one, so the field has no key.
	Language string `toml:"-"`
	// Grype is true for a job whose command scans what it built with
	// grype and writes the findings as SARIF to grypeSARIF. The
	// workflow then uploads the file to code scanning under the
	// category grype/<component>.
	Grype bool `toml:"grype"`
}

// Toolchains are the setups the workflow template knows.
var toolchains = []string{"go", "hugo", "rust", "prek", "os"}

// grypeSARIF is the file that a job with grype = true writes its scan
// to. The upload step reads the file by this name, so a command that
// writes another file uploads nothing.
func grypeSARIF(c *Component) string { return "$GITHUB_WORKSPACE/grype/" + c.Name() + ".sarif" }

// validateJob checks one job of the component's package.toml.
func (c *Component) validateJob(path string, job Job) error {
	if job.Name == "" || (job.Run == "") != (job.Toolchain == "prek") {
		return fmt.Errorf("%s: every job needs a name, and a run command unless prek runs it", path)
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
		return fmt.Errorf("%s: job %q: a sharded job needs coverage files for its shards to hand to it", path, job.Name)
	}
	if job.Grype && !strings.Contains(job.Run, grypeSARIF(c)) {
		return fmt.Errorf("%s: job %q: a job with grype = true writes its scan to %s, and its run command names no such file", path, job.Name, grypeSARIF(c))
	}
	if !job.Grype && strings.Contains(job.Run, grypeSARIF(c)) {
		return fmt.Errorf("%s: job %q: the run command writes a grype scan that no step uploads; set grype = true", path, job.Name)
	}
	return nil
}
