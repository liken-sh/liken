package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

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
		// The key ends in the commit, so each push to main saves the
		// test results and packages of that commit, and a restore takes
		// the newest entry with the same flags, then the job's newest.
		"key: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}-${{ github.sha }}\n" +
			"          restore-keys: |\n" +
			"            go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}-\n" +
			"            go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-\n",
		// Every manual reads the same modules, so the manuals share one.
		"key: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-${{ hashFiles('app/docs/go.sum') }}\n" +
			"          restore-keys: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-\n",
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}-${{ github.sha }}\n" +
			"          restore-keys: |\n            hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}-\n            hugo-build-${{ runner.os }}-\n",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.go-modules.outputs.cache-hit != 'true' }}\n        uses: actions/cache/save@v6",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.go-build.outputs.cache-hit != 'true' }}\n        uses: actions/cache/save@v6",
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

// A build cache's key holds the hash of the files that choose what the
// job compiles and with which flags: its go.sum, the Makefile where it
// runs, and the component's package.toml and hooks. A restore takes the
// newest entry with the same hash first.
func TestABuildCacheFollowsWhatChoosesTheBuild(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\ndir = \"svc\"\nrun = \"make test\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n",
		"app/go.mod":                  "module app\n",
		"app/.pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: go-vet\n        entry: go vet ./...\n",
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
		"${{ github.job }}-${{ hashFiles('app/svc/go.sum', 'app/svc/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}-${{ github.sha }}\n",
		"${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}-${{ github.sha }}\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks the key %q:\n%s", want, text)
		}
	}
}

// cacheSteps describes each step of a job that restores or saves the
// Actions cache, as its action, its key, and its paths.
func cacheSteps(t *testing.T, job map[string]any) []string {
	t.Helper()
	var found []string
	for _, step := range job["steps"].([]any) {
		step := step.(map[string]any)
		uses, _ := step["uses"].(string)
		with, _ := step["with"].(map[string]any)
		if strings.HasPrefix(uses, "actions/cache/") {
			found = append(found, fmt.Sprintf("%s %v %v", uses, with["key"], with["path"]))
		}
	}
	return found
}

// The repository's cache holds 10 GB, so each module's downloaded
// source is one entry, keyed on go.sum, that every job of the module
// restores and only the `go` job saves. The entry leaves out the zip
// files, which `go` does not read after it extracts the source. Each
// job's build cache holds the compiled output alone.
func TestEveryJobOfAModuleSharesOneModulesEntry(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n",
		"app/go.mod":                  "module app\n",
		"app/.pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: go-vet\n        entry: go vet ./...\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	const (
		modulePaths    = "~/go/pkg/mod/*\n!~/go/pkg/mod/cache\n~/go/pkg/mod/cache/download/**/*.mod\n~/go/pkg/mod/cache/download/**/*.ziphash\n"
		restoreModules = "actions/cache/restore@v6 go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ hashFiles('app/go.sum') }} " + modulePaths
		restoreBuild   = "actions/cache/restore@v6 go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}-${{ github.sha }} ~/.cache/go-build"
		saveModules    = "actions/cache/save@v6 ${{ steps.go-modules.outputs.cache-primary-key }} " + modulePaths
		saveBuild      = "actions/cache/save@v6 ${{ steps.go-build.outputs.cache-primary-key }} ~/.cache/go-build"
	)
	tests := []struct {
		job  string
		want []string
	}{
		{"go", []string{restoreModules, restoreBuild, saveModules, saveBuild}},
		{"prek", []string{restoreModules, restoreBuild, saveBuild}},
	}
	for _, tt := range tests {
		t.Run(tt.job, func(t *testing.T) {
			if got := cacheSteps(t, jobs[tt.job]); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// The OS's build sets up Go in the build-setup action, which cannot save
// after the build, so the job saves the action's build cache itself. The
// OS's `go` job saves the modules.
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
	save := "      - if: ${{ github.ref == 'refs/heads/main' && steps.build-setup.outputs.go-build-hit != 'true' }}\n        uses: actions/cache/save@v6\n        with:\n          path: ~/.cache/go-build\n"
	if strings.Count(text, "id: build-setup\n        uses: ./.github/actions/build-setup") != 2 || strings.Count(text, save) != 2 {
		t.Errorf("the build and the publish do not both save the action's Go cache:\n%s", text)
	}
}
