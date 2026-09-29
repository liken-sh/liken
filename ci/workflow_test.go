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
	if _, ok := jobs["os-publish"]["secrets"]; !ok {
		t.Error("the OS's publish call does not pass the secrets")
	}
	if _, ok := jobs["os"]["secrets"]; ok {
		t.Error("the OS's checks get the secrets")
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
		"fluxcd/flux2/action@v2.9.5",
		"go run . publish -root .. -component app",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks %q", want)
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
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(files[".github/workflows/component-ci.yaml"]); !strings.Contains(text, "\nname: component-ci\n") {
		t.Errorf("the ci component's workflow is not named component-ci:\n%s", text)
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
		// Each job has a build cache of its own: a job that builds less
		// must not save the entry that another job then restores whole.
		"key: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum') }}\n" +
			"          restore-keys: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-\n",
		// Every manual reads the same modules, so the manuals share one.
		"key: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-${{ hashFiles('app/docs/go.sum') }}\n" +
			"          restore-keys: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-\n",
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}-${{ github.sha }}\n" +
			"          restore-keys: |\n            hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}-\n            hugo-build-${{ runner.os }}-\n",
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

// A component's hooks run in CI only through a prek job, so a
// component with hooks and no prek job is refused.
func TestAComponentWithHooksNeedsAPrekJob(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n",
		"app/.pre-commit-config.yaml": "repos: []\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "no prek job") {
		t.Errorf("got %v", err)
	}
}

// A prek job skips the hooks that another job of the component runs.
func TestAPrekJobSkipsTheHooksThatOtherJobsRun(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test-go\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\nskip = [\"test-go\", \"test-docs\"]\n",
		"app/.pre-commit-config.yaml": "repos: []\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "      - uses: j178/prek-action@v2\n        env:\n          SKIP: test-go,test-docs\n"
	if text := string(files[".github/workflows/component-app.yaml"]); !strings.Contains(text, want) {
		t.Errorf("the workflow lacks %q:\n%s", want, text)
	}
}

func TestOnlyAPrekJobSkipsHooks(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\nskip = [\"x\"]\n",
	})
	if _, err := LoadComponents(root); err == nil || !strings.Contains(err.Error(), "only a prek job") {
		t.Errorf("got %v", err)
	}
}

// A component's checks wait only for the pinned bases it builds on,
// whose layers its images read from the cache that the bases' jobs
// write. Its publish waits for the checks of every component in its
// closure, so it never publishes on a dependency that failed.
func TestTheChecksWaitForTheBasesAndThePublishWaitsForTheClosure(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
		"lib/package.toml":  "[package]\nname = \"lib\"\n",
		"app/package.toml":  "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\", \"lib\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"top/package.toml":  "[package]\nname = \"top\"\n[depends]\ncomponents = [\"app\"]\n[outputs]\ndeploy = \"deploy\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	cases := map[string][]any{
		"app":          {"plan", "base"},
		"top":          {"plan"},
		"lib":          {"plan"},
		"base-publish": {"plan", "base"},
		"app-publish":  {"plan", "app", "base", "lib"},
		"top-publish":  {"plan", "top", "app", "base", "lib"},
	}
	for name, needs := range cases {
		if got := jobs[name]["needs"]; !reflect.DeepEqual(got, needs) {
			t.Errorf("%s needs %v, want %v", name, got, needs)
		}
	}
	if _, ok := jobs["lib-publish"]; ok {
		t.Error("a component with no outputs has a publish call")
	}
	publish := jobs["app-publish"]
	if publish["uses"] != "./.github/workflows/component-app.yaml" ||
		publish["if"] != "${{ !failure() && !cancelled() && fromJSON(needs.plan.outputs.components)['app'].publish != 'none' }}" ||
		publish["with"].(map[string]any)["stage"] != "publish" || jobs["app"]["with"].(map[string]any)["stage"] != "check" {
		t.Errorf("app's publish call is %v", publish)
	}
	component := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	if needs, ok := component["publish"]["needs"]; ok || component["publish"]["if"] != "${{ inputs.stage == 'publish' || inputs.dryrun }}" {
		t.Errorf("the publish job needs %v, if %v", needs, component["publish"]["if"])
	}
}

// The check stage runs the publish job in its dry mode when the plan
// asks for a dry run. The dry run logs in to nothing, and the OS's dry
// run builds and boots a release under the lab's serial and uploads
// nothing.
func TestTheCheckStageRunsADryRunOfThePublish(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"app\"\n",
		"os/package.toml":  "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	calls := workflowJobs(t, files[".github/workflows/ci.yaml"])
	if with := calls["app"]["with"].(map[string]any); with["dryrun"] != "${{ fromJSON(needs.plan.outputs.components)['app'].dryrun }}" {
		t.Errorf("the check call passes dryrun %v", with["dryrun"])
	}
	if with := calls["app-publish"]["with"].(map[string]any); with["dryrun"] != false {
		t.Errorf("the publish call passes dryrun %v", with["dryrun"])
	}
	app := string(files[".github/workflows/component-app.yaml"])
	for _, want := range []string{
		"  publish:\n    if: ${{ inputs.stage == 'publish' || inputs.dryrun }}\n",
		"      - if: ${{ !inputs.dryrun }}\n        uses: docker/login-action@v3",
		`-mode "${{ inputs.dryrun && 'dry' || inputs.publish }}"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the app's workflow lacks %q", want)
		}
	}
	os := string(files[".github/workflows/component-os.yaml"])
	for _, want := range []string{
		`if [ "${{ inputs.dryrun }}" = true ]; then version="$(date -u +%Y.%m.%d)-000"; fi`,
		"      - if: ${{ !inputs.dryrun }}\n        uses: actions/download-artifact@v8",
		"      - name: publish to the channel\n        if: ${{ !inputs.dryrun }}\n",
		"      - name: check the publish script\n        if: ${{ inputs.dryrun }}\n",
	} {
		if !strings.Contains(os, want) {
			t.Errorf("the OS's workflow lacks %q", want)
		}
	}
}

func TestThePublishJobSetsUpOnlyWhatTheOutputsNeed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"operator/package.toml": "[package]\nname = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\n",
		"crd/package.toml":      "[package]\nname = \"crd\"\n[outputs]\ndeploy = \"deploy\"\n",
		"base/package.toml":     "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"operator": {"docker/setup-buildx-action@v3", "docker/login-action@v3", "fluxcd/flux2/action@v2.9.5"},
		"crd":      {"docker/login-action@v3", "fluxcd/flux2/action@v2.9.5"},
		"base":     {"docker/setup-buildx-action@v3", "docker/login-action@v3"},
	}
	for name, want := range cases {
		publish := workflowJobs(t, files[".github/workflows/component-"+name+".yaml"])["publish"]
		var got []string
		for _, step := range publish["steps"].([]any) {
			uses, _ := step.(map[string]any)["uses"].(string)
			if strings.HasPrefix(uses, "docker/") || strings.HasPrefix(uses, "fluxcd/") {
				got = append(got, uses)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s's publish job sets up %v, want %v", name, got, want)
		}
	}
}
