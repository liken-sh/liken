package main

import (
	"reflect"
	"strings"
	"testing"
)

// codeqlFiles is a repository with two components. screen has a Go
// job, a Rust job for its crate in ui/, C sources in layout/, and a
// Python smoke check. lib is a Go module that screen's go.mod
// replaces, with a C file of its own.
var codeqlFiles = map[string]string{
	"lib/package.toml": "[package]\nname = \"lib\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n",
	"lib/go.mod":       "module example.com/lib\n",
	"lib/lib.go":       "package lib\n",
	"lib/native.c":     "int lib(void) { return 0; }\n",
	"screen/package.toml": `[package]
name = "screen"
[depends]
components = ["lib"]
[[jobs]]
name = "go"
toolchain = "go"
run = "make test-go"
[[jobs]]
name = "rust"
toolchain = "rust"
dir = "ui"
run = "make test"
`,
	"screen/go.mod":           "module example.com/screen\n\nreplace example.com/lib => ../lib\n",
	"screen/main.go":          "package main\n",
	"screen/README.md":        "a screen\n",
	"screen/ui/Cargo.toml":    "[package]\nname = \"ui\"\n",
	"screen/ui/src/lib.rs":    "pub fn ui() {}\n",
	"screen/layout/layout.c":  "int layout(void) { return 0; }\n",
	"screen/layout/layout.h":  "int layout(void);\n",
	"screen/smoke/check.py":   "print('ok')\n",
	"screen/testdata/fake.py": "print('a fixture')\n",
}

// The loader derives one CodeQL job for each Go and Rust job, and one
// for each other language whose sources a component holds. A file in
// testdata/ is a fixture, and it derives no job.
func TestTheLoaderDerivesACodeQLJobForEachAnalysis(t *testing.T) {
	components, err := LoadComponents(writeTree(t, codeqlFiles))
	if err != nil {
		t.Fatal(err)
	}
	type derived struct{ Name, Language, Dir, Run string }
	cases := []struct {
		component string
		jobs      []derived
	}{
		{"screen", []derived{
			{"codeql-go", "go", "", "go build ./..."},
			{"codeql-rust", "rust", "ui", ""},
			{"codeql-c-cpp", "c-cpp", "", ""},
			{"codeql-python", "python", "", ""},
		}},
		{"lib", []derived{
			{"codeql-go", "go", "", "go build ./..."},
			{"codeql-c-cpp", "c-cpp", "", ""},
		}},
	}
	for _, c := range cases {
		t.Run(c.component, func(t *testing.T) {
			var got []derived
			for _, job := range components[c.component].Jobs {
				if job.Toolchain == "codeql" {
					got = append(got, derived{job.Name, job.Language, job.Dir, job.Run})
				}
			}
			if !reflect.DeepEqual(got, c.jobs) {
				t.Errorf("got %+v, want %+v", got, c.jobs)
			}
		})
	}
}

// A declared job cannot take a derived job's name.
func TestAPackageTomlCannotDeclareACodeQLJob(t *testing.T) {
	root := writeTree(t, map[string]string{"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"codeql-go\"\ntoolchain = \"go\"\nrun = \"true\"\n"})
	if _, err := LoadComponents(root); err == nil || !strings.Contains(err.Error(), "codeql-") {
		t.Errorf("got %v, want an error that names the prefix", err)
	}
}

// A CodeQL job runs only when a source of its language changes in its
// own directory, or, for Go and Rust, in a module or a crate that the
// job it comes from builds with. A header is a C source. A smoke check
// runs no Go or Rust job, and it runs the Python analysis.
func TestACodeQLJobRunsWhenASourceOfItsLanguageChanges(t *testing.T) {
	cases := []struct {
		name string
		file string
		jobs []string
	}{
		{"a Go source", "screen/main.go", []string{"go", "codeql-go"}},
		{"a Rust source", "screen/ui/src/lib.rs", []string{"rust", "codeql-rust"}},
		{"a C source", "screen/layout/layout.c", []string{"go", "rust", "codeql-c-cpp"}},
		{"a C header", "screen/layout/layout.h", []string{"go", "rust", "codeql-c-cpp"}},
		{"a Python smoke check", "screen/smoke/check.py", []string{"codeql-python"}},
		{"a file that no analysis reads", "screen/README.md", []string{"go", "rust"}},
		{"a Go source of a module that go.mod replaces", "lib/lib.go", []string{"go", "codeql-go"}},
		{"a C source of a module that go.mod replaces", "lib/native.c", []string{"go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, codeqlFiles)
			before := r.run("rev-parse", "HEAD")
			r.write(c.file, "changed\n")
			r.commit("the change")
			d := planGate(t, r, nil, Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})["screen"]
			if !reflect.DeepEqual(d.Jobs, c.jobs) {
				t.Errorf("screen: jobs %v, want %v", d.Jobs, c.jobs)
			}
		})
	}
}

// The Actions code is every file under .github/, so the analysis of it
// runs when one of them changes, and not for a change elsewhere.
func TestTheActionsAnalysisRunsWhenAFileUnderGitHubChanges(t *testing.T) {
	cases := []struct {
		name, file string
		runs       bool
		why        string
	}{
		{"a workflow", ".github/workflows/cert.yaml", true, "changed: .github/workflows/cert.yaml"},
		{"a local action", ".github/actions/setup/action.yaml", true, "changed: .github/actions/setup/action.yaml"},
		{"a component's source", "screen/main.go", false, "no file under .github/ changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, codeqlFiles)
			before := r.run("rev-parse", "HEAD")
			r.write(c.file, "changed\n")
			r.commit("the change")
			runs, why, err := r.planner(nil).Actions(Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})
			if err != nil {
				t.Fatal(err)
			}
			if runs != c.runs || why != c.why {
				t.Errorf("got %v, %q; want %v, %q", runs, why, c.runs, c.why)
			}
		})
	}
}

// codeqlStep finds the step of a job that uses the action, and returns
// its inputs.
func codeqlStep(t *testing.T, job map[string]any, action string) map[string]any {
	t.Helper()
	for _, step := range job["steps"].([]any) {
		step := step.(map[string]any)
		if uses, _ := step["uses"].(string); strings.HasPrefix(uses, action+"@") {
			with, _ := step["with"].(map[string]any)
			return with
		}
	}
	t.Fatalf("no step uses %s: %v", action, job["steps"])
	return nil
}

// Each derived job analyzes its own directory and uploads under a
// category of its own, with the one permission that the upload needs.
func TestEachCodeQLJobUploadsUnderItsOwnCategory(t *testing.T) {
	root := writeTree(t, codeqlFiles)
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/component-screen.yaml"])
	cases := []struct {
		job, language, mode, paths, category string
	}{
		{"codeql-go", "go", "manual", "screen\n", "codeql/screen/go"},
		{"codeql-rust", "rust", "none", "screen/ui\n", "codeql/screen/rust"},
		{"codeql-c-cpp", "c-cpp", "none", "screen\n", "codeql/screen/c-cpp"},
		{"codeql-python", "python", "none", "screen\n", "codeql/screen/python"},
	}
	for _, c := range cases {
		t.Run(c.job, func(t *testing.T) {
			job := jobs[c.job]
			if want := map[string]any{"contents": "read", "security-events": "write"}; !reflect.DeepEqual(job["permissions"], want) {
				t.Errorf("permissions %v", job["permissions"])
			}
			init := codeqlStep(t, job, "github/codeql-action/init")
			if init["languages"] != c.language || init["build-mode"] != c.mode || init["config"] != "paths:\n  - "+c.paths {
				t.Errorf("init %v", init)
			}
			if analyze := codeqlStep(t, job, "github/codeql-action/analyze"); analyze["category"] != c.category {
				t.Errorf("analyze %v", analyze)
			}
		})
	}
}

// A called workflow's jobs take no permission that the call does not
// grant, so the call of a component with a CodeQL job grants the
// upload, and the root workflow analyzes the Actions code itself.
func TestTheRootWorkflowGrantsTheUploadsAndAnalyzesTheActions(t *testing.T) {
	root := writeTree(t, codeqlFiles)
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	if got := jobs["screen"]["permissions"].(map[string]any)["security-events"]; got != "write" {
		t.Errorf("the call of screen grants security-events: %v", got)
	}
	actions := jobs["codeql-actions"]
	if init := codeqlStep(t, actions, "github/codeql-action/init"); init["languages"] != "actions" || init["config"] != "paths:\n  - .github\n" {
		t.Errorf("init %v", init)
	}
	if analyze := codeqlStep(t, actions, "github/codeql-action/analyze"); analyze["category"] != "codeql/actions" {
		t.Errorf("analyze %v", analyze)
	}
}
