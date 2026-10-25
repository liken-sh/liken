package main

import (
	"slices"
	"strings"
	"testing"
)

func TestADeployNeedsTheFluxVersion(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "liken/flux/VERSION") {
		t.Errorf("got %v, want an error that names liken/flux/VERSION", err)
	}
}

// Every prek job and the repository job run one prek, so a hook
// behaves the same in each.
func TestEveryPrekJobRunsOnePrek(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{".github/workflows/ci.yaml", ".github/workflows/component-app.yaml"} {
		if want := "prek-version: " + prekVersion + "\n"; !strings.Contains(string(files[file]), want) {
			t.Errorf("%s lacks %q", file, want)
		}
	}
}

// The graph job runs on every push, so a pin that drifts fails the
// commit that makes it drift.
func TestTheGraphJobChecksThePins(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, step := range workflowJobs(t, files[".github/workflows/ci.yaml"])["graph"]["steps"].([]any) {
		run, _ := step.(map[string]any)["run"].(string)
		runs = append(runs, run)
	}
	if !slices.Contains(runs, "go run . pins -root ..") {
		t.Errorf("the graph job runs %q", runs)
	}
}
