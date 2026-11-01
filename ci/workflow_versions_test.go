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

// prek-action verifies a download only against its own checksum table,
// and it runs a prek that the table does not name unverified. So every
// prek step passes the digest that this repository pins.
func TestEveryPrekStepVerifiesThePinnedDigest(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n"})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
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

// setup-trivy installs Trivy with an install script that checks the
// archive against the checksums file of the same release, read at run
// time, so a changed release changes what CI runs. So each step that
// installs Trivy checks the archive against the digest that this
// repository pins.
func TestEveryTrivyInstallVerifiesThePinnedDigest(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatal(err)
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
			t.Errorf("%s installs Trivy with setup-trivy", file)
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
		t.Error("no step installs Trivy")
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
