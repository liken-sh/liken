package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestADeployNeedsTheFluxVersion(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "liken/flux/VERSION") {
		t.Errorf("got %v, want an error that names liken/flux/VERSION", err)
	}
}

// Every prek job and the repository job of ci.yaml install the same
// prek version, so a hook behaves the same in each.
func TestEveryPrekJobRunsOnePrek(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	for _, file := range []string{".github/workflows/ci.yaml", ".github/workflows/component-app.yaml"} {
		if want := "prek-version: " + prekVersion + "\n"; !strings.Contains(string(files[file]), want) {
			t.Errorf("%s does not contain %q", file, want)
		}
	}
}

// prek-action checks a download only against a checksum table built
// into the action, and runs a prek version that is not in the table
// with no check. So every prek step passes the pinned sha256.
func TestEveryPrekStepVerifiesThePinnedDigest(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(prekSHA256) {
		t.Fatalf("prekSHA256 is %q, want a SHA-256 digest", prekSHA256)
	}
	for _, step := range []struct{ file, job string }{
		{".github/workflows/ci.yaml", "repository"},
		{".github/workflows/component-app.yaml", "prek"},
	} {
		steps := workflowJobs(t, files[step.file])[step.job]["steps"].([]any)
		prek := steps[len(steps)-1].(map[string]any)
		if got := prek["with"].(map[string]any)["sha256"]; got != prekSHA256 {
			t.Errorf("the %s job of %s passes sha256 %v, want %s", step.job, step.file, got, prekSHA256)
		}
	}
}

// setup-trivy's install script checks the archive against the
// release's checksums file, which it downloads at run time, so a
// changed release changes what CI runs. So no workflow uses
// setup-trivy, and each step that installs Trivy passes the pinned
// TRIVY_SHA256.
func TestEveryTrivyInstallVerifiesThePinnedDigest(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(trivySHA256) {
		t.Fatalf("trivySHA256 is %q, want a SHA-256 digest", trivySHA256)
	}
	installs := 0
	for file, text := range files {
		if !strings.HasPrefix(file, ".github/workflows/") {
			continue
		}
		if strings.Contains(string(text), "setup-trivy") {
			t.Errorf("%s installs Trivy with setup-trivy, want an install that checks TRIVY_SHA256", file)
		}
		for job, fields := range workflowJobs(t, text) {
			steps, _ := fields["steps"].([]any)
			for _, step := range steps {
				env, _ := step.(map[string]any)["env"].(map[string]any)
				if env["TRIVY_VERSION"] == nil {
					continue
				}
				installs++
				if env["TRIVY_SHA256"] != trivySHA256 {
					t.Errorf("the %s job of %s installs Trivy with TRIVY_SHA256 %v, want %s", job, file, env["TRIVY_SHA256"], trivySHA256)
				}
			}
		}
	}
	if installs == 0 {
		t.Error("no step of the generated workflows installs Trivy, want at least one")
	}
}

// The graph job runs on every push, so a commit that breaks a pin rule
// fails its own CI run.
func TestTheGraphJobChecksThePins(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	var runs []string
	for _, step := range workflowJobs(t, files[".github/workflows/ci.yaml"])["graph"]["steps"].([]any) {
		run, _ := step.(map[string]any)["run"].(string)
		runs = append(runs, run)
	}
	if !slices.Contains(runs, "go run . pins -root ..") {
		t.Errorf("the graph job runs %q, want it to run \"go run . pins -root ..\"", runs)
	}
}
