package main

import (
	"reflect"
	"testing"
)

// graphFixture is a small repository: an OS that depends on brand, a
// base image component, and an app that builds on the base and has a
// manual. The OS and the base each exclude their monitoring/, and the
// base's ignore file keeps it out of the image.
func graphFixture(t *testing.T) map[string]*Component {
	t.Helper()
	root := writeTree(t, map[string]string{
		"brand/package.toml":               "[package]\nname = \"brand\"\n",
		"base/package.toml":                "[package]\nname = \"base\"\n[outputs]\nexclude = [\"monitoring/\"]\n[[outputs.images]]\nname = \"base\"\n",
		"base/.dockerignore":               "monitoring/\n",
		"base/monitoring/podmonitor.yaml":  "",
		"app/package.toml":                 "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\"]\n[docs]\nprefix = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"os/package.toml":                  "[package]\nname = \"os\"\n[depends]\ncomponents = [\"brand\"]\n[outputs]\nchannel = true\nexclude = [\"monitoring/\"]\n",
		"os/monitoring/kustomization.yaml": "",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	return components
}

func TestTheClosureFollowsEveryDependency(t *testing.T) {
	components := graphFixture(t)
	if got := Closure(components, "app"); !reflect.DeepEqual(got, []string{"app", "base"}) {
		t.Errorf("closure %v", got)
	}
}

func TestACheckRunsWhenItsPathsChange(t *testing.T) {
	components := graphFixture(t)
	cases := []struct {
		name, component, file string
		runs                  bool
	}{
		{"its own file", "app", "app/main.go", true},
		{"its dependency's file", "app", "base/Dockerfile", true},
		{"brand, for a component with a manual", "app", "brand/voice.md", true},
		{"brand's plans, for a component with a manual", "app", "brand/plans/01.md", false},
		{"another component's file", "app", "os/init/main.go", false},
		{"its own manual", "app", "app/docs/index.md", true},
		{"its own test", "app", "app/main_test.go", true},
		{"its own smoke check", "app", "app/smoke/app.sh", true},
		{"its own skills", "app", "app/skills/install/SKILL.md", true},
		{"its own plans", "app", "app/plans/01.md", false},
		{"its own hooks", "app", "app/.pre-commit-config.yaml", true},
		{"its own coverage floor", "app", "app/.testcoverage.yml", true},
		{"its dependency's hooks", "app", "base/.pre-commit-config.yaml", false},
		{"its dependency's coverage floor", "app", "base/.testcoverage.yml", false},
		{"its dependency's git ignore file", "app", "base/.gitignore", false},
		{"its dependency's docker ignore file", "app", "base/.dockerignore", true},
		{"its own path that no output is built from", "os", "os/monitoring/kustomization.yaml", true},
		{"its dependency's manual", "app", "base/docs/index.md", false},
		{"its dependency's notes", "app", "base/README.md", false},
		{"its dependency's plans", "app", "base/plans/01.md", false},
		{"its dependency's test", "app", "base/pkg/pkg_test.go", false},
		{"its dependency's test data", "app", "base/pkg/testdata/case.json", false},
		{"its dependency's smoke check", "app", "base/smoke/base.sh", false},
		{"its dependency's skills", "app", "base/skills/install/SKILL.md", false},
		{"its dependency's excluded path", "app", "base/monitoring/podmonitor.yaml", false},
		{"the excluded path of a component with an image", "base", "base/monitoring/podmonitor.yaml", true},
		{"its dependency's source", "app", "base/pkg/pkg.go", true},
		{"a nested README in its dependency, which is source", "app", "base/deploy/README.md", true},
		{"a file whose name only ends like a test", "app", "base/pkg/contest.go", true},
		{"brand, for a component with no manual", "base", "brand/voice.md", false},
		{"a file at the top", "app", "README.md", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if runs, _ := CheckChanged(components, c.component, Diff{Files: []string{c.file}}); runs != c.runs {
				t.Errorf("CheckChanged(%s, %s) = %v", c.component, c.file, runs)
			}
		})
	}
}

func TestAReleaseChangeIsAChangeToAnOutput(t *testing.T) {
	components := graphFixture(t)
	cases := []struct {
		name, component, file, why string
	}{
		{"its own file", "app", "app/main.go", "changed: app/main.go"},
		{"its dependency's file", "app", "base/Dockerfile", "its dependency base changed: base/Dockerfile"},
		{"its manual", "app", "app/docs/index.md", ""},
		{"its plans", "app", "app/plans/01.md", ""},
		{"its agent notes", "app", "app/AGENTS.md", ""},
		{"a nested README is source", "app", "app/deploy/README.md", "changed: app/deploy/README.md"},
		{"a test", "app", "app/pkg/pkg_test.go", ""},
		{"its hooks", "app", "app/.pre-commit-config.yaml", ""},
		{"its dependency's hooks", "app", "base/.pre-commit-config.yaml", ""},
		{"its coverage floor", "app", "app/.testcoverage.yml", ""},
		{"its git ignore file", "app", "app/.gitignore", ""},
		{"a docker ignore file, which shapes an image", "app", "base/.dockerignore", "its dependency base changed: base/.dockerignore"},
		{"a nested hooks file is source", "app", "app/cmd/.pre-commit-config.yaml", "changed: app/cmd/.pre-commit-config.yaml"},
		{"test data", "app", "app/pkg/testdata/case.json", ""},
		{"its smoke check", "app", "app/smoke/app.sh", ""},
		{"its skills", "app", "app/skills/install/SKILL.md", ""},
		{"its dependency's skills", "app", "base/skills/install/SKILL.md", ""},
		{"a path its package.toml excludes", "os", "os/monitoring/kustomization.yaml", ""},
		{"a path its dependency's package.toml excludes", "app", "base/monitoring/podmonitor.yaml", ""},
		{"a path excluded beside an image", "base", "base/monitoring/podmonitor.yaml", ""},
		{"the same path in a component that does not exclude it", "app", "app/monitoring/podmonitor.yaml", "changed: app/monitoring/podmonitor.yaml"},
		{"an excluded directory's name as a file", "os", "os/monitoring.yaml", "changed: os/monitoring.yaml"},
		{"a nested smoke directory is source", "app", "app/cmd/smoke/main.go", "changed: app/cmd/smoke/main.go"},
		{"its dependency's test", "app", "base/pkg/pkg_test.go", ""},
		{"brand, which it does not depend on", "app", "brand/liken.css", ""},
		{"brand, which it depends on", "os", "brand/liken.css", "its dependency brand changed: brand/liken.css"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changed, why := ReleaseChanged(components, c.component, Diff{Files: []string{c.file}})
			if changed != (c.why != "") || why != c.why {
				t.Errorf("ReleaseChanged(%s, %s) = %v, %q", c.component, c.file, changed, why)
			}
		})
	}
}

func TestTheDependencyOrderPutsEachComponentAfterItsDependencies(t *testing.T) {
	components := graphFixture(t)
	var names []string
	for _, c := range dependencyOrder(components) {
		names = append(names, c.Name())
	}
	if want := []string{"base", "app", "brand", "os"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order %v, want %v", names, want)
	}
}
