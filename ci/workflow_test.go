package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTheWorkflowsAreWrittenAndChecked(t *testing.T) {
	components := graphFixture(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, ".github/workflows/component-gone.yaml")
	os.WriteFile(stale, []byte(generatedMark+" from gone/package.toml.\n"), 0o644)
	handWritten := filepath.Join(root, ".github/workflows/cert.yaml")
	os.WriteFile(handWritten, []byte("name: cert\n"), 0o644)

	files, err := Workflows(components)
	if err != nil {
		t.Fatal(err)
	}
	differ, err := CheckWorkflows(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(differ) != len(files)+1 {
		t.Errorf("before writing, %v differ", differ)
	}
	if err := WriteWorkflows(root, files); err != nil {
		t.Fatal(err)
	}
	if differ, _ := CheckWorkflows(root, files); len(differ) != 0 {
		t.Errorf("after writing, %v differ", differ)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the workflow of a removed component stayed")
	}
	if _, err := os.Stat(handWritten); err != nil {
		t.Error("a hand-written workflow was removed")
	}
	os.WriteFile(filepath.Join(root, ".github/workflows/ci.yaml"), []byte(generatedMark+" by hand\n"), 0o644)
	if differ, _ := CheckWorkflows(root, files); !reflect.DeepEqual(differ, []string{".github/workflows/ci.yaml"}) {
		t.Errorf("after an edit, %v differ", differ)
	}
}

// workflowJobs parses a generated workflow and returns its jobs.
func workflowJobs(t *testing.T, data []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	return doc.Jobs
}

func TestTheRootWorkflowCallsEachComponentAfterItsDependencies(t *testing.T) {
	files, err := Workflows(graphFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	app := jobs["app"]
	if app["uses"] != "./.github/workflows/component-app.yaml" {
		t.Errorf("app uses %v", app["uses"])
	}
	if needs := app["needs"]; !reflect.DeepEqual(needs, []any{"plan", "base"}) {
		t.Errorf("app needs %v", needs)
	}
	if _, ok := jobs["os"]["secrets"]; !ok {
		t.Error("the OS's call does not pass the secrets")
	}
	if _, ok := jobs["app"]["secrets"]; ok {
		t.Error("an operator's call passes the secrets")
	}
	if !strings.Contains(string(files[".github/workflows/ci.yaml"]), "fetch https://liken.sh/app/ > page.html") {
		t.Error("the site check does not fetch the app's manual")
	}
}

func TestAComponentWorkflowHasItsJobsItsImagesAndItsPublish(t *testing.T) {
	root := writeTree(t, map[string]string{
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
smoke = "app/smoke/app.sh"
[[outputs.images]]
name = "app-browser"
contexts = { brand = "brand", media = "media" }
[[outputs.images]]
name = "app-cli"
file = "Dockerfile.cli"
platforms = ["linux/amd64", "linux/arm64"]
`,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-app.yaml"])
	jobs := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	for _, name := range []string{"go", "docs", "images", "publish"} {
		if _, ok := jobs[name]; !ok {
			t.Errorf("no %s job", name)
		}
	}
	if needs := jobs["publish"]["needs"]; !reflect.DeepEqual(needs, []any{"go", "docs", "images"}) {
		t.Errorf("publish needs %v", needs)
	}
	for _, want := range []string{
		"go-version-file: app/docs/go.mod",
		"sudo apt-get install -y --no-install-recommends ffmpeg",
		"      - run: make test-go\n",
		"      - run: |\n          make test\n          make build\n",
		"name: coverage-app-go",
		"platforms: linux/amd64,linux/arm64\n            load: false",
		"smoke: 'app/smoke/app.sh'",
		`build-contexts: "brand=brand\nmedia=media"`,
		"fluxcd/flux2/action@v2.9.5",
		"go run . publish -root .. -component app",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks %q", want)
		}
	}
}

func TestACoverageFileMustBeAtTheComponentsTop(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"m\"\ncoverage = [\"sub/coverage.out\"]\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Workflows(components); err == nil || !strings.Contains(err.Error(), "at the component's top") {
		t.Errorf("got %v", err)
	}
}
