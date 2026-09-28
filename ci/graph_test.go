package main

import (
	"reflect"
	"testing"
)

// graphFixture is a small repository: an OS that depends on brand, a
// base image component, and an app that builds on the base and has a
// manual.
func graphFixture(t *testing.T) map[string]*Component {
	t.Helper()
	root := writeTree(t, map[string]string{
		"brand/package.toml": "[package]\nname = \"brand\"\n",
		"base/package.toml":  "[package]\nname = \"base\"\n[[outputs.images]]\nname = \"base\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\"]\n[docs]\nprefix = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"os/package.toml":    "[package]\nname = \"os\"\n[depends]\ncomponents = [\"brand\"]\n[outputs]\nchannel = true\n",
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
		{"a workflow", "app", ".github/workflows/ci.yaml", true},
		{"another component's file", "app", "os/init/main.go", false},
		{"brand, for a component with no manual", "base", "brand/voice.md", false},
		{"a file at the top", "app", "README.md", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if runs, _ := CheckChanged(components, c.component, []string{c.file}); runs != c.runs {
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
		{"brand, which it does not depend on", "app", "brand/liken.css", ""},
		{"brand, which it depends on", "os", "brand/liken.css", "its dependency brand changed: brand/liken.css"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changed, why := ReleaseChanged(components, c.component, []string{c.file})
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
