package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fluxTree is a repository that contains only the flux version, which the
// publish job of a component with a deploy artifact reads.
func fluxTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{"liken/flux/VERSION": testFlux + "\n", "toolchains/images.toml": fixtureBuildKitImages})
}

func TestTheWorkflowsAreWrittenAndChecked(t *testing.T) {
	components := graphFixture(t)
	root := fluxTree(t)
	if err := os.MkdirAll(filepath.Join(root, ".github/workflows"), 0o755); err != nil {
		t.Fatalf("creating the workflows directory: %v", err)
	}
	stale := filepath.Join(root, ".github/workflows/component-gone.yaml")
	os.WriteFile(stale, []byte(generatedMark+" from gone/package.toml.\n"), 0o644)
	handWritten := filepath.Join(root, ".github/workflows/cert.yaml")
	os.WriteFile(handWritten, []byte("name: cert\n"), 0o644)

	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	differ, err := CheckWorkflows(root, files)
	if err != nil {
		t.Fatalf("checking the workflows: %v", err)
	}
	if len(differ) != len(files)+1 {
		t.Errorf("before writing, the files that differ are %v, want all %d generated files and the stale one", differ, len(files))
	}
	if err := WriteWorkflows(root, files); err != nil {
		t.Fatalf("writing the workflows: %v", err)
	}
	if differ, _ := CheckWorkflows(root, files); len(differ) != 0 {
		t.Errorf("after writing, the files that differ are %v, want none", differ)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the workflow of a removed component is still there, want it deleted")
	}
	if _, err := os.Stat(handWritten); err != nil {
		t.Error("a hand-written workflow was deleted, want it kept")
	}
	os.WriteFile(filepath.Join(root, ".github/workflows/ci.yaml"), []byte(generatedMark+" by hand\n"), 0o644)
	if differ, _ := CheckWorkflows(root, files); !reflect.DeepEqual(differ, []string{".github/workflows/ci.yaml"}) {
		t.Errorf("after an edit, the files that differ are %v, want only ci.yaml", differ)
	}
}

// workflowJobs parses a generated workflow and returns its jobs.
func workflowJobs(t *testing.T, data []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing the generated workflow: %v\n%s", err, data)
	}
	return doc.Jobs
}

func TestTheRootWorkflowCallsEachComponentAfterItsDependencies(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	app := jobs["app"]
	if app["uses"] != "./.github/workflows/component-app.yaml" {
		t.Errorf("the call of app uses %v, want ./.github/workflows/component-app.yaml", app["uses"])
	}
	if _, ok := jobs["os-publish"]["secrets"]; !ok {
		t.Error("the call of os-publish passes no secrets, want them passed")
	}
	if _, ok := jobs["os"]["secrets"]; ok {
		t.Error("the call of os passes the secrets, want none")
	}
	for name, images := range map[string]bool{"base": true, "app": false} {
		with := jobs[name]["with"].(map[string]any)
		if with["jobs"] != "${{ toJSON(fromJSON(needs.plan.outputs.components)['"+name+"'].jobs) }}" {
			t.Errorf("the call of %s passes the jobs %v, want the jobs that the plan selects for it", name, with["jobs"])
		}
		if _, ok := with["images"]; ok != images {
			t.Errorf("the call of %s passes images is %v, want %v", name, ok, images)
		}
	}
	if _, ok := jobs["app"]["secrets"]; ok {
		t.Error("the call of app passes the secrets, want none")
	}
	if !strings.Contains(string(files[".github/workflows/ci.yaml"]), "fetch https://liken.sh/app/ > page.html") {
		t.Error("the site check does not fetch the manual of app, want it fetched")
	}
}

func TestAComponentWorkflowHasItsJobsItsImagesAndItsPublish(t *testing.T) {
	root := writeTree(t, map[string]string{
		"toolchains/images.toml": fixtureBuildKitImages,
		"app/package.toml": `[package]
name = "app"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test-go"
coverage = ["coverage.out"]
apt = ["ffmpeg"]
[[jobs]]
name = "docs"
toolchain = "hugo"
module = "docs"
run = """
make test
make build
"""
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "app"
description = "The app image."
smoke = "app/smoke/app.sh"
[[outputs.images]]
name = "app-browser"
description = "The app-browser image."
contexts = { brand = "brand", media = "media" }
[[outputs.images]]
name = "app-cli"
description = "The app-cli image."
file = "Dockerfile.cli"
platforms = ["linux/amd64", "linux/arm64"]
`,
		"liken/flux/VERSION": testFlux + "\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	text := string(files[".github/workflows/component-app.yaml"])
	jobs := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	for _, name := range []string{"go", "docs", "images", "publish"} {
		if _, ok := jobs[name]; !ok {
			t.Errorf("the workflow of app has no %s job, want one", name)
		}
	}
	for _, want := range []string{
		"go-version-file: app/docs/go.mod",
		"sudo apt-get install -y --no-install-recommends ffmpeg",
		"      - run: make test-go\n",
		"      - run: |\n          make test\n          make build\n",
		"name: coverage-app-go",
		"  go:\n    if: ${{ inputs.stage == 'check' && contains(fromJSON(inputs.jobs), 'go') }}\n",
		"  images:\n    name: ${{ matrix.image }}\n    if: ${{ inputs.stage == 'check' && fromJSON(inputs.images)[0] != null }}\n",
		"include: ${{ fromJSON(inputs.images) }}",
		"targets: ${{ matrix.image }}",
		"BRANCH_CACHE=${{ matrix.image }}",
		pinnedText("fluxcd/flux2/action") + "\n        with:\n          version: " + strings.TrimPrefix(testFlux, "v") + "\n",
		"go run . publish -root .. -component app",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow of app does not contain %q, want it", want)
		}
	}
}

// A component workflow's name must differ from the root workflow's, or a
// component named ci gives two workflows one name, and `gh run list
// --workflow ci` cannot tell them apart.
func TestAComponentWorkflowIsNamedApartFromTheRootWorkflow(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ci/package.toml": "[package]\nname = \"ci\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	if text := string(files[".github/workflows/component-ci.yaml"]); !strings.Contains(text, "\nname: component-ci\n") {
		t.Errorf("the workflow of the ci component does not have the name component-ci:\n%s", text)
	}
}

func TestACoverageFileMustBeAtTheComponentsTop(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"m\"\ncoverage = [\"sub/coverage.out\"]\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "at the component's top") {
		t.Errorf("Workflows returned %v, want an error that says a coverage file must be at the component's top", err)
	}
}
