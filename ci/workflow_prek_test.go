package main

import (
	"strings"
	"testing"
)

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

// The graph job runs `generate -check`, so the repository job skips the
// root hook that runs the same check. prek ignores the SKIP variable
// when the command line has a `--skip`, and the repository job skips
// each component's directory, so the hook's skip is an argument too.
func TestTheRepositoryJobSkipsTheWorkflowsHook(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	steps := jobs["repository"]["steps"].([]any)
	prek := steps[len(steps)-1].(map[string]any)
	args := prek["with"].(map[string]any)["extra-args"].(string) + " "
	if prek["uses"] != "j178/prek-action@v2" || !strings.Contains(args, " --skip workflows ") {
		t.Errorf("the repository job's prek step is %v", prek)
	}
}

// A prek job sets up Go only when a hook that it runs in CI runs go or
// gofmt. A hook that the job skips runs in another job, which sets up
// Go for itself, and a remote hook installs its own language.
func TestAPrekJobSetsUpGoOnlyForAHookThatRunsGo(t *testing.T) {
	hooks := "repos:\n" +
		"  - repo: https://github.com/pre-commit/pre-commit-hooks\n    rev: v6.0.0\n    hooks:\n      - id: trailing-whitespace\n" +
		"  - repo: local\n    hooks:\n" +
		"      - id: gofmt\n        name: gofmt\n        entry: bash -c 'exec \"$(go env GOROOT)/bin/gofmt\" -l -w \"$@\"' --\n        language: system\n" +
		"      - id: go-vet\n        name: go vet\n        entry: go vet ./...\n        language: system\n" +
		"      - id: cargo-fmt\n        name: cargo fmt\n        entry: bash -c 'cargo fmt --check'\n        language: system\n"
	for _, tc := range []struct {
		name, skip string
		goMod      bool
		setsUpGo   bool
	}{
		{"every hook runs", `[]`, true, true},
		{"one Go hook runs", `["go-vet"]`, true, true},
		{"every Go hook is skipped", `["gofmt", "go-vet"]`, true, false},
		{"no go.mod", `[]`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"app/package.toml":            "[package]\nname = \"app\"\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\nskip = " + tc.skip + "\n",
				"app/.pre-commit-config.yaml": hooks,
			}
			if tc.goMod {
				files["app/go.mod"] = "module app\n"
			}
			root := writeTree(t, files)
			components, err := LoadComponents(root)
			if err != nil {
				t.Fatal(err)
			}
			workflows, err := Workflows(root, components)
			if err != nil {
				t.Fatal(err)
			}
			text := string(workflows[".github/workflows/component-app.yaml"])
			if got := strings.Contains(text, "uses: actions/setup-go@"); got != tc.setsUpGo {
				t.Errorf("the prek job sets up Go: %v, want %v:\n%s", got, tc.setsUpGo, text)
			}
		})
	}
}
