package main

import (
	"bytes"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// rootWorkflowFile is the generated workflow that calls every
// component's workflow.
const rootWorkflowFile = ".github/workflows/ci.yaml"

// A Diff is what changed between two commits, in the terms that the
// plan decides with. The generated files at the top of the repository
// hold a part for each component, so a change to one of them names the
// components whose part changed, not every component.
type Diff struct {
	// Files are the changed files, less the generated workflows, the
	// bake file, and the other files under .github/.
	Files []string
	// Workflows maps each component whose workflow changed to the file
	// that changed: its own generated workflow, its call in the root
	// workflow, or a local action that its workflow uses.
	Workflows map[string]string
	// Targets holds each image whose target in the bake file changed.
	Targets map[string]bool
	// SharedBake is true when the bake file changed outside its
	// targets, such as a variable that every target reads.
	SharedBake bool
}

// componentWorkflow matches the path of a component's generated
// workflow, and names the component.
var componentWorkflow = regexp.MustCompile(`^\.github/workflows/component-(.+)\.yaml$`)

// ReadDiff reads what changed between two commits.
//
// A file under .github/ that is not a component's part runs no
// component's jobs: the root workflow's own jobs run in every run, and
// no component's jobs read any other file there. A changed local action
// runs each component whose workflow uses any local action, because
// one action can use another.
func (g Git) ReadDiff(components map[string]*Component, from, to string) (Diff, error) {
	d := Diff{Workflows: map[string]string{}, Targets: map[string]bool{}}
	files, err := g.Changed(from, to)
	if err != nil {
		return d, err
	}
	action := ""
	for _, file := range files {
		switch {
		case file == rootWorkflowFile:
			if err := g.rootWorkflowDiff(components, from, to, &d); err != nil {
				return d, err
			}
		case componentWorkflow.MatchString(file):
			name := componentWorkflow.FindStringSubmatch(file)[1]
			if _, ok := components[name]; ok {
				d.Workflows[name] = file
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

// rootWorkflowDiff names each component whose calls in the root
// workflow differ between the two commits: the call of its checks, or
// the call of its publish. A root workflow that is
// missing or does not parse at either commit names every component.
func (g Git) rootWorkflowDiff(components map[string]*Component, from, to string, d *Diff) error {
	var calls [2]map[string][]byte
	for i, rev := range []string{from, to} {
		text, found, err := g.Show(rev, rootWorkflowFile)
		if err != nil {
			return err
		}
		if found {
			calls[i] = workflowCalls(text)
		}
	}
	for name := range components {
		for _, job := range []string{name, name + "-publish"} {
			if calls[0] == nil || calls[1] == nil || !bytes.Equal(calls[0][job], calls[1][job]) {
				d.Workflows[name] = rootWorkflowFile
			}
		}
	}
	return nil
}

// workflowCalls maps each job of a workflow to its text, or returns nil
// when the workflow does not parse.
func workflowCalls(text string) map[string][]byte {
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

// bakeDiff names each target of the bake file that differs between the
// two commits, and whether the part outside the targets differs. A bake
// file that is missing at either commit differs everywhere.
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
// file, and names its kind and its label.
var bakeBlock = regexp.MustCompile(`^(target|group|variable) "([^"]+)" \{$`)

// splitBake splits the bake file into the text of each target and the
// text that every target shares: the variables and any other line
// outside a block. The default group names the targets for a build of
// every image on a workstation, and no build in CI reads it, so it is
// in neither part; comments outside a block are in neither part too.
//
// The file is generated, and `ci generate -check` holds it to the
// template, so every top-level block starts on a line of its own at
// the left margin and ends with a line that holds only "}".
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
