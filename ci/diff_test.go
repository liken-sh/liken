package main

import (
	"reflect"
	"strings"
	"testing"
)

// rootWorkflow is a root workflow in the shape that ci.yaml.tmpl
// writes: shared jobs, and one call for each component.
func rootWorkflow(calls map[string]string, plan string) string {
	var b strings.Builder
	b.WriteString("# generated\nname: ci\non:\n  push:\njobs:\n  plan:\n    runs-on: " + plan + "\n")
	for _, name := range []string{"base", "operator", "liken", "brand"} {
		if with, ok := calls[name]; ok {
			b.WriteString("  " + name + ":\n    uses: ./.github/workflows/component-" + name + ".yaml\n    with:\n      version: " + with + "\n")
		}
	}
	b.WriteString("  site:\n    runs-on: ubuntu\n")
	return b.String()
}

var everyCall = map[string]string{"base": "v", "operator": "v", "liken": "v", "brand": "v"}

// bakeFileText is a bake file in the shape that docker-bake.hcl.tmpl
// writes: variables, the default group, and one target for each image.
func bakeFileText(variable string, targets map[string]string) string {
	var b strings.Builder
	b.WriteString("# generated\nvariable \"VERSION\" {\n  default = \"" + variable + "\"\n}\n\ngroup \"default\" {\n  targets = [\n")
	for _, name := range []string{"base", "operator"} {
		if _, ok := targets[name]; ok {
			b.WriteString("    \"" + name + "\",\n")
		}
	}
	b.WriteString("  ]\n}\n")
	for _, name := range []string{"base", "operator"} {
		if platforms, ok := targets[name]; ok {
			b.WriteString("\n# the image " + name + "\ntarget \"" + name + "\" {\n  context = \"" + name + "\"\n  platforms = [\"" + platforms + "\"]\n}\n")
		}
	}
	return b.String()
}

var everyTarget = map[string]string{"base": "linux/amd64", "operator": "linux/amd64"}

// generatedFixture is planFixture with the generated files at the top
// of the repository, and a push that follows.
func generatedFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := planFixture(t)
	r.write(".github/workflows/ci.yaml", rootWorkflow(everyCall, "ubuntu"))
	for _, name := range []string{"base", "operator", "brand"} {
		r.write(".github/workflows/component-"+name+".yaml", "# generated\nname: "+name+"\n")
	}
	r.write(".github/workflows/component-liken.yaml", "# generated\nname: liken\njobs:\n  build:\n    steps:\n      - uses: ./.github/actions/build-setup\n")
	r.write(".github/actions/build-setup/action.yml", "runs:\n  using: composite\n")
	r.write(".github/dependabot.yml", "version: 2\n")
	r.write(bakeFile, bakeFileText("check", everyTarget))
	return r, r.commit("the generated files")
}

// planPush plans a push to a topic branch from the commit before.
func planPush(t *testing.T, r *repo, before string) map[string]Decision {
	t.Helper()
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	return decisions
}

func checked(decisions map[string]Decision) []string {
	var names []string
	for _, c := range []string{"base", "brand", "liken", "operator"} {
		if decisions[c].Check {
			names = append(names, c)
		}
	}
	return names
}

func TestAGeneratedFileRunsOnlyTheComponentsWhosePartChanged(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(r *repo)
		check []string
	}{
		{"a component's own workflow", func(r *repo) {
			r.write(".github/workflows/component-operator.yaml", "# generated\nname: operator\non: push\n")
		}, []string{"operator"}},
		{"a component's call in the root workflow", func(r *repo) {
			r.write(".github/workflows/ci.yaml", rootWorkflow(map[string]string{"base": "v", "operator": "w", "liken": "v", "brand": "v"}, "ubuntu"))
		}, []string{"operator"}},
		{"a component's publish call in the root workflow", func(r *repo) {
			r.write(".github/workflows/ci.yaml", rootWorkflow(everyCall, "ubuntu")+"  operator-publish:\n    uses: ./.github/workflows/component-operator.yaml\n")
		}, []string{"operator"}},
		{"a shared job of the root workflow", func(r *repo) {
			r.write(".github/workflows/ci.yaml", rootWorkflow(everyCall, "ubuntu-next"))
		}, nil},
		{"a local action, for each component whose workflow uses one", func(r *repo) {
			r.write(".github/actions/build-setup/action.yml", "runs:\n  using: composite\n  steps: []\n")
		}, []string{"liken"}},
		{"another file under .github", func(r *repo) {
			r.write(".github/dependabot.yml", "version: 3\n")
		}, nil},
		{"the workflow of a component that no longer exists", func(r *repo) {
			r.write(".github/workflows/component-gone.yaml", "# generated\nname: gone\n")
		}, nil},
		{"an image's bake target, for its component and every component that builds on it", func(r *repo) {
			r.write(bakeFile, bakeFileText("check", map[string]string{"base": "linux/arm64", "operator": "linux/amd64"}))
		}, []string{"base", "operator"}},
		{"a variable of the bake file, for every component with an image", func(r *repo) {
			r.write(bakeFile, bakeFileText("other", everyTarget))
		}, []string{"base", "operator"}},
		{"only the default group of the bake file", func(r *repo) {
			r.write(bakeFile, strings.Replace(bakeFileText("check", everyTarget), "    \"base\",\n", "", 1))
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := generatedFixture(t)
			c.edit(r)
			r.commit("the change")
			if got := checked(planPush(t, r, before)); !reflect.DeepEqual(got, c.check) {
				t.Errorf("the checks that run: %v, want %v", got, c.check)
			}
		})
	}
}

func TestARootWorkflowThatWasNotThereRunsEveryCall(t *testing.T) {
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write(".github/workflows/ci.yaml", rootWorkflow(everyCall, "ubuntu"))
	r.commit("the first root workflow")
	if got := checked(planPush(t, r, before)); !reflect.DeepEqual(got, []string{"base", "brand", "liken", "operator"}) {
		t.Errorf("the checks that run: %v", got)
	}
}

func TestACallThatIsNewInTheRootWorkflowRunsItsComponent(t *testing.T) {
	r, _ := generatedFixture(t)
	r.write(".github/workflows/ci.yaml", rootWorkflow(map[string]string{"operator": "v", "liken": "v", "brand": "v"}, "ubuntu"))
	before := r.commit("a root workflow without the base")
	r.write(".github/workflows/ci.yaml", rootWorkflow(everyCall, "ubuntu"))
	r.commit("call the base")
	if got := checked(planPush(t, r, before)); !reflect.DeepEqual(got, []string{"base"}) {
		t.Errorf("the checks that run: %v", got)
	}
}

func TestARootWorkflowThatDoesNotParseRunsEveryCall(t *testing.T) {
	r, before := generatedFixture(t)
	r.write(".github/workflows/ci.yaml", "jobs: [\n")
	r.commit("a broken root workflow")
	if got := checked(planPush(t, r, before)); !reflect.DeepEqual(got, []string{"base", "brand", "liken", "operator"}) {
		t.Errorf("the checks that run: %v", got)
	}
}

func TestABakeTargetChangeIsAChangeToTheOutputs(t *testing.T) {
	r, _ := generatedFixture(t)
	r.write(bakeFile, bakeFileText("check", map[string]string{"base": "linux/arm64", "operator": "linux/amd64"}))
	r.commit("build the base for another platform")
	decisions, err := r.planner(fixturePublished).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; !d.Changed || !strings.HasPrefix(d.Why, "its dependency base changed: the bake target base") {
		t.Errorf("operator: %+v", d)
	}
}

func TestASharedBakeChangeIsNotAChangeToTheOutputs(t *testing.T) {
	r, _ := generatedFixture(t)
	r.run("tag", "operator/2026.09.29-001")
	r.write(bakeFile, bakeFileText("other", everyTarget))
	r.commit("change a default")
	decisions, err := r.planner(map[string][]string{"operator": {"2026.09.29-001"}}).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; d.Changed {
		t.Errorf("operator: %+v", d)
	}
}

// A rename shows in a diff under its new name alone unless rename
// detection is off, and the component that lost the file must run too.
func TestAFileMovedBetweenComponentsRunsBoth(t *testing.T) {
	r, before := generatedFixture(t)
	r.run("mv", "operator/main.go", "liken/main.go")
	r.commit("move a file")
	if got := checked(planPush(t, r, before)); !reflect.DeepEqual(got, []string{"liken", "operator"}) {
		t.Errorf("the checks that run: %v", got)
	}
}
