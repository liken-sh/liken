package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Only a push to main saves to the Actions cache, so a branch never
// evicts one of main's entries. The manuals that share a go.sum share
// one Hugo build cache.
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
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	text := string(files[".github/workflows/component-app.yaml"])
	for _, want := range []string{
		"          cache: false\n",
		// Each job has its own build cache, so a job that builds less
		// does not save an entry that another job then restores. The
		// key has no commit, so main saves a new entry only when the
		// hashed files change. A restore tries the exact key first,
		// then the newest entry of the same job.
		"key: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}\n" +
			"          restore-keys: go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-\n",
		// Every manual reads the same modules, so the manuals share one
		// modules entry.
		"key: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-${{ hashFiles('app/docs/go.sum') }}\n" +
			"          restore-keys: go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-manuals-\n",
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('app/docs/go.sum') }}\n" +
			"          restore-keys: hugo-build-${{ runner.os }}-\n",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.go-modules.outputs.cache-hit != 'true' }}\n        uses: " + pinnedText("actions/cache/save"),
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.go-build.outputs.cache-hit != 'true' }}\n        uses: " + pinnedText("actions/cache/save"),
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.hugo-cache.outputs.cache-hit != 'true' }}\n        uses: " + pinnedText("actions/cache/save"),
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

// A build cache's key hashes the files that decide what the job
// compiles and with which flags: the go.sum and Makefile of the job's
// directory, and the component's package.toml and hooks.
func TestABuildCacheFollowsWhatChoosesTheBuild(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\ndir = \"svc\"\nrun = \"make test\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n",
		"app/go.mod":                  "module app\n",
		"app/.pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: go-vet\n        entry: go vet ./...\n",
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
	for _, want := range []string{
		"${{ github.job }}-${{ hashFiles('app/svc/go.sum', 'app/svc/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}\n",
		"${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }}\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks the key %q:\n%s", want, text)
		}
	}
}

// A cache key that contains the commit misses on every push to main, so
// every run on main saves a new entry, and the entries evict each other
// from the repository's 10 GB cache. No key contains the commit, so main
// saves an entry only when the inputs of its key change.
func TestNoCacheKeyContainsTheCommit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n[[jobs]]\nname = \"docs\"\ntoolchain = \"hugo\"\nmodule = \"docs\"\nrun = \"make docs\"\n",
		"os/package.toml":  "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	for name, data := range files {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "key") && strings.Contains(line, "github.sha") {
				t.Errorf("%s: a cache key holds the commit: %s", name, strings.TrimSpace(line))
			}
		}
	}
}

// cacheSteps returns "<uses> <key> <path>" for each step of a job that
// restores or saves the Actions cache.
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

// The repository's Actions cache is limited to 10 GB, so each module's
// downloaded source is one entry, keyed on go.sum, that every job of
// the module restores and only the `go` job saves. The entry leaves out
// the zip files, which `go` does not read after it extracts the source.
func TestEveryJobOfAModuleSharesOneModulesEntry(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n",
		"app/go.mod":                  "module app\n",
		"app/.pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: go-vet\n        entry: go vet ./...\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	jobs := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	var (
		modulePaths    = "~/go/pkg/mod/*\n!~/go/pkg/mod/cache\n~/go/pkg/mod/cache/download/**/*.mod\n~/go/pkg/mod/cache/download/**/*.ziphash\n"
		restoreModules = pinnedRef("actions/cache/restore") + " go-modules-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ hashFiles('app/go.sum') }} " + modulePaths
		restoreBuild   = pinnedRef("actions/cache/restore") + " go-build-${{ runner.os }}-${{ steps.setup-go.outputs.go-version }}-app-${{ github.job }}-${{ hashFiles('app/go.sum', 'app/Makefile', 'app/package.toml', 'app/.pre-commit-config.yaml') }} ~/.cache/go-build"
		saveModules    = pinnedRef("actions/cache/save") + " ${{ steps.go-modules.outputs.cache-primary-key }} " + modulePaths
		saveBuild      = pinnedRef("actions/cache/save") + " ${{ steps.go-build.outputs.cache-primary-key }} ~/.cache/go-build"
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
				t.Errorf("the cache steps are %q, want %q", got, tt.want)
			}
		})
	}
}

// The OS jobs set up Go in the build-setup action, which cannot save
// after the build, so the build and publish jobs save the action's Go
// build cache themselves.
func TestTheOSBuildSavesItsGoCacheOnMain(t *testing.T) {
	root := writeTree(t, map[string]string{
		"os/package.toml": "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	text := string(files[".github/workflows/component-os.yaml"])
	save := "      - if: ${{ github.ref == 'refs/heads/main' && steps.build-setup.outputs.go-build-hit != 'true' }}\n        uses: " + pinnedText("actions/cache/save") + "\n        with:\n          path: ~/.cache/go-build\n"
	if strings.Count(text, "id: build-setup\n        uses: ./.github/actions/build-setup") != 2 || strings.Count(text, save) != 2 {
		t.Errorf("the build and the publish do not both save the action's Go cache:\n%s", text)
	}
}
