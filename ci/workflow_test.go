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

	files, err := Workflows(root, components)
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
	files, err := Workflows("", graphFixture(t))
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
	for name, images := range map[string]bool{"base": true, "app": false} {
		with := jobs[name]["with"].(map[string]any)
		if with["jobs"] != "${{ toJSON(fromJSON(needs.plan.outputs.components)['"+name+"'].jobs) }}" {
			t.Errorf("%s's call passes the jobs %v", name, with["jobs"])
		}
		if _, ok := with["images"]; ok != images {
			t.Errorf("%s's call passes images: %v", name, ok)
		}
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
	files, err := Workflows(root, components)
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
		"  go:\n    if: ${{ contains(fromJSON(inputs.jobs), 'go') }}\n",
		"  images:\n    name: ${{ matrix.image }}\n    if: ${{ inputs.images != '[]' }}\n",
		"include: ${{ fromJSON(inputs.images) }}",
		"targets: ${{ matrix.image }}",
		"BRANCH_CACHE=${{ matrix.image }}",
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
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "at the component's top") {
		t.Errorf("got %v", err)
	}
}

func TestAHooksJobSetsUpGoOnlyForAGoModule(t *testing.T) {
	root := writeTree(t, map[string]string{
		"os/package.toml":   "[package]\nname = \"os\"\n[[jobs]]\nname = \"checks\"\ntoolchain = \"prek\"\n",
		"os/go.mod":         "module os\n",
		"base/package.toml": "[package]\nname = \"base\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{"os": true, "base": false}
	for name, wants := range cases {
		t.Run(name, func(t *testing.T) {
			text := string(files[".github/workflows/component-"+name+".yaml"])
			if strings.Contains(text, "actions/setup-go") != wants {
				t.Errorf("setup-go in the %s workflow is %v:\n%s", name, !wants, text)
			}
		})
	}
}

func TestAPinnedImageJobLogsInOnEveryBranch(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-base.yaml"])
	for _, want := range []string{"|| matrix.pinned }}\n        uses: docker/login-action@v3"} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks %q:\n%s", want, text)
		}
	}
}

// The site takes a coverage profile only from a job that ran, because a
// job that did not run uploaded nothing.
func TestTheSiteTakesCoverageOnlyFromAJobThatRan(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"m\"\ncoverage = [\"coverage.out\"]\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "if: ${{ fromJSON(needs.plan.outputs.components)['app'].check && contains(fromJSON(needs.plan.outputs.components)['app'].jobs, 'go') }}\n        with:\n          name: coverage-app-go"
	if !strings.Contains(string(files[".github/workflows/ci.yaml"]), want) {
		t.Errorf("the site's download lacks %q", want)
	}
}

// Only a push to main writes the Actions cache, so a branch never
// pushes one of main's entries out, and the manuals that share a go.sum
// share one Hugo build cache.
func TestOnlyMainSavesTheCaches(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": `[package]
name = "app"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test"
[[jobs]]
name = "docs"
toolchain = "hugo"
module = "docs"
run = "make docs"
[[jobs]]
name = "rust"
toolchain = "rust"
dir = "ui"
run = "make test"
`,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-app.yaml"])
	for _, want := range []string{
		"          cache: false\n",
		"key: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-${{ hashFiles('app/go.sum') }}",
		"key: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-${{ hashFiles('app/docs/go.sum') }}",
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}-${{ github.sha }}",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.go-cache.outputs.cache-hit != 'true' }}\n        uses: actions/cache/save@v6",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.hugo-cache.outputs.cache-hit != 'true' }}\n        uses: actions/cache/save@v6",
		"          key: app\n          save-if: ${{ github.ref == 'refs/heads/main' }}\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks %q", want)
		}
	}
	for _, unwanted := range []string{"cache-dependency-path", "uses: actions/cache@"} {
		if strings.Contains(text+string(files[".github/workflows/ci.yaml"]), unwanted) {
			t.Errorf("a workflow holds %q, which saves on every branch", unwanted)
		}
	}
}

// The OS's build sets up Go in the build-setup action, which cannot save
// after the build, so the job saves the action's cache itself.
func TestTheOSBuildSavesItsGoCacheOnMain(t *testing.T) {
	root := writeTree(t, map[string]string{
		"os/package.toml": "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-os.yaml"])
	save := "      - if: ${{ github.ref == 'refs/heads/main' && steps.build-setup.outputs.go-cache-hit != 'true' }}\n        uses: actions/cache/save@v6"
	if strings.Count(text, "id: build-setup\n        uses: ./.github/actions/build-setup") != 2 || strings.Count(text, save) != 2 {
		t.Errorf("the build and the publish do not both save the action's Go cache:\n%s", text)
	}
}
