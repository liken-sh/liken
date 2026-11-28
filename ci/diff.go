package main

import (
	"bytes"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// rootWorkflowFile is the generated workflow that calls every
// component's workflow.
const rootWorkflowFile = ".github/workflows/ci.yaml"

// A Diff is what changed between two commits, sorted into the kinds of
// change that the planner selects jobs by. The generated root workflow and
// bake file hold a section for each component, so a change to one of
// them marks only the components whose section changed.
type Diff struct {
	// Files are the changed files, less the generated workflows, the
	// bake file, and the other files under .github/.
	Files []string
	// Checks maps each changed package.toml to true when only its jobs
	// or its outputs.exclude paths changed. Such a change runs the
	// component's checks and does not change the version of any output.
	Checks map[string]bool
	// Workflows maps each component whose workflow changed to the file
	// that changed: its own generated workflow, its call in the root
	// workflow, or a local action that its workflow uses.
	Workflows map[string]string
	// Publishes maps each component whose publish code changed to the
	// file that changed: the publish job of its workflow, or its publish
	// call in the root workflow.
	Publishes map[string]string
	// Targets is true for each image whose target in the bake file
	// changed.
	Targets map[string]bool
	// SharedBake is true when the bake file changed outside its targets,
	// for example in a variable that every target reads.
	SharedBake bool
	// Site is true when the site job or the site-deploy job of the root
	// workflow changed.
	Site bool
	// GitHub is the first changed file under .github/, or "" when none
	// changed. The CodeQL analysis of the Actions code reads those files.
	GitHub string
}

// componentWorkflow matches the path of a component's generated
// workflow, and captures the component name.
var componentWorkflow = regexp.MustCompile(`^\.github/workflows/component-(.+)\.yaml$`)

// ReadDiff reads what changed between two commits.
//
// A file under .github/ other than a component's workflow, the root
// workflow, or a local action runs no component's jobs, because no
// component's jobs read it. A changed local action marks every
// component whose workflow uses any local action, because one local
// action can call another.
func (g Git) ReadDiff(components map[string]*Component, from, to string) (Diff, error) {
	d := Diff{Workflows: map[string]string{}, Publishes: map[string]string{}, Targets: map[string]bool{}, Checks: map[string]bool{}}
	files, err := g.Changed(from, to)
	if err != nil {
		return d, err
	}
	action := ""
	for _, file := range files {
		if strings.HasPrefix(file, githubDir) && d.GitHub == "" {
			d.GitHub = file
		}
		switch {
		case file == rootWorkflowFile:
			if err := g.rootWorkflowDiff(components, from, to, &d); err != nil {
				return d, err
			}
		case componentWorkflow.MatchString(file):
			name := componentWorkflow.FindStringSubmatch(file)[1]
			if _, ok := components[name]; ok {
				d.Workflows[name] = file
				changed, err := g.jobChanged(from, to, file, "publish")
				if err != nil {
					return d, err
				}
				if changed {
					d.Publishes[name] = file
				}
			}
		case strings.HasPrefix(file, ".github/actions/"):
			action = file
		case strings.HasPrefix(file, ".github/"):
		case file == bakeFile:
			if err := g.bakeDiff(from, to, &d); err != nil {
				return d, err
			}
		default:
			d.Files = append(d.Files, file)
			if path.Base(file) == packageFile {
				checks, err := g.checksOnly(from, to, file)
				if err != nil {
					return d, err
				}
				d.Checks[file] = checks
			}
		}
	}
	if action != "" {
		for _, c := range sortedComponents(components) {
			text, _, err := g.Show(to, ".github/workflows/component-"+c.Name()+".yaml")
			if err != nil {
				return d, err
			}
			if strings.Contains(text, "uses: ./.github/actions/") {
				d.Workflows[c.Name()] = action
			}
		}
	}
	return d, nil
}

// outputFiles returns Files without each package.toml that Checks
// marks true, because that change does not change the version of any output.
func (d Diff) outputFiles() []string {
	return slices.DeleteFunc(slices.Clone(d.Files), func(file string) bool { return d.Checks[file] })
}

// checksOnly is true when a package.toml differs between the two commits
// only in its jobs and its excluded paths. A file that is missing at
// either commit or does not parse counts as a change to everything in
// it.
func (g Git) checksOnly(from, to, file string) (bool, error) {
	var parts [2]map[string]any
	for i, rev := range []string{from, to} {
		text, found, err := g.Show(rev, file)
		if err != nil || !found {
			return false, err
		}
		if _, err := toml.Decode(text, &parts[i]); err != nil {
			return false, nil
		}
		delete(parts[i], "jobs")
		// A package.toml whose outputs table has only the excluded paths
		// equals one with no outputs table.
		if outputs, ok := parts[i]["outputs"].(map[string]any); ok {
			delete(outputs, "exclude")
			if len(outputs) == 0 {
				delete(parts[i], "outputs")
			}
		}
	}
	return reflect.DeepEqual(parts[0], parts[1]), nil
}

// rootWorkflowDiff records each component whose checks call or publish
// call in the root workflow differs between the two commits, and
// whether the site job or the site-deploy job differs. A root workflow
// that is missing or does not parse at either commit counts as a change
// to every component and the site.
func (g Git) rootWorkflowDiff(components map[string]*Component, from, to string, d *Diff) error {
	var calls [2]map[string][]byte
	for i, rev := range []string{from, to} {
		text, found, err := g.Show(rev, rootWorkflowFile)
		if err != nil {
			return err
		}
		if found {
			calls[i] = jobTexts(text)
		}
	}
	for _, job := range []string{"site", "site-deploy"} {
		if calls[0] == nil || calls[1] == nil || !bytes.Equal(calls[0][job], calls[1][job]) {
			d.Site = true
		}
	}
	for name := range components {
		for _, job := range []string{name, name + "-publish"} {
			if calls[0] == nil || calls[1] == nil || !bytes.Equal(calls[0][job], calls[1][job]) {
				d.Workflows[name] = rootWorkflowFile
				if job != name {
					d.Publishes[name] = rootWorkflowFile
				}
			}
		}
	}
	return nil
}

// jobChanged is true when the job of a workflow differs between the two
// commits, or when the workflow is missing or does not parse at either
// one.
func (g Git) jobChanged(from, to, file, job string) (bool, error) {
	var jobs [2]map[string][]byte
	for i, rev := range []string{from, to} {
		text, found, err := g.Show(rev, file)
		if err != nil {
			return false, err
		}
		if found {
			jobs[i] = jobTexts(text)
		}
	}
	return jobs[0] == nil || jobs[1] == nil || !bytes.Equal(jobs[0][job], jobs[1][job]), nil
}

// jobTexts maps each job of a workflow to its text, or returns nil
// when the workflow does not parse.
func jobTexts(text string) map[string][]byte {
	var doc struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	if yaml.Unmarshal([]byte(text), &doc) != nil || doc.Jobs == nil {
		return nil
	}
	calls := map[string][]byte{}
	for name, node := range doc.Jobs {
		data, err := yaml.Marshal(&node)
		if err != nil {
			return nil
		}
		calls[name] = data
	}
	return calls
}

// bakeDiff records each target of the bake file that differs between
// the two commits, and whether the text outside the targets differs. A
// bake file that is missing at either commit counts as different
// everywhere.
func (g Git) bakeDiff(from, to string, d *Diff) error {
	var parts [2]bakeParts
	for i, rev := range []string{from, to} {
		text, found, err := g.Show(rev, bakeFile)
		if err != nil {
			return err
		}
		if !found {
			d.SharedBake = true
		}
		parts[i] = splitBake(text)
	}
	if parts[0].shared != parts[1].shared {
		d.SharedBake = true
	}
	for _, name := range append(slices.Collect(maps.Keys(parts[0].targets)), slices.Collect(maps.Keys(parts[1].targets))...) {
		if parts[0].targets[name] != parts[1].targets[name] {
			d.Targets[name] = true
		}
	}
	return nil
}

type bakeParts struct {
	targets map[string]string
	shared  string
}

// bakeBlock matches the first line of a top-level block of the bake
// file, and captures its kind and its label.
var bakeBlock = regexp.MustCompile(`^(target|group|variable) "([^"]+)" \{$`)

// splitBake splits the bake file into the text of each target and the
// text that every target shares: the variables and any other line
// outside a block. It leaves out the groups, because the default group
// lists every image for a build on a workstation and no build in CI
// reads it. It also leaves out comments outside a block.
//
// The parse works only on the generated file: `ci generate -check`
// keeps the file equal to the template, so every top-level block starts
// at the left margin and ends with a line that is only "}".
func splitBake(text string) bakeParts {
	p := bakeParts{targets: map[string]string{}}
	var shared strings.Builder
	kind, name := "", ""
	var block strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if kind == "" {
			if m := bakeBlock.FindStringSubmatch(line); m != nil {
				kind, name = m[1], m[2]
				block.Reset()
				block.WriteString(line + "\n")
				continue
			}
			if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				shared.WriteString(line + "\n")
			}
			continue
		}
		block.WriteString(line + "\n")
		if line != "}" {
			continue
		}
		switch kind {
		case "target":
			p.targets[name] = block.String()
		case "variable":
			shared.WriteString(block.String())
		}
		kind = ""
	}
	if kind != "" {
		shared.WriteString(block.String())
	}
	p.shared = shared.String()
	return p
}
