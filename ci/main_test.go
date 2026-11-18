package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// channel is a test server for a release channel's versions.yaml.
func channel(t *testing.T, versions string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/versions.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(versions))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

const fixtureVersions = `apiVersion: liken.sh/v1alpha1
kind: Versions
latest: 2026.09.28-001
releases:
  - version: 2026.09.28-001
    digest: sha256:abc
`

// fakeEnv is an Env of fixtures: a registry, a channel, environment
// variables, and a recorder in place of the commands it would run.
type fakeEnv struct {
	Env
	vars     map[string]string
	out      *bytes.Buffer
	err      *bytes.Buffer
	recorded *recorder
	// signed holds each image reference whose signature verifies.
	signed map[string]bool
}

func newFakeEnv(t *testing.T, fake *fakeRegistry) *fakeEnv {
	t.Helper()
	registry := fake.serve(t)
	w := &fakeEnv{vars: map[string]string{}, out: &bytes.Buffer{}, err: &bytes.Buffer{}, recorded: &recorder{registry: fake}, signed: map[string]bool{}}
	w.Env = Env{
		Published: Published{Registry: registry, Channel: channel(t, fixtureVersions), Client: http.DefaultClient},
		Registry: func(token string) Registry {
			r := registry
			r.Token = token
			return r
		},
		Run: w.recorded.run,
		VerifyImage: func(root, reference, identity string) error {
			if !w.signed[reference] || !strings.Contains(identity, "component-") {
				return fmt.Errorf("no signature of %s verifies", identity)
			}
			return nil
		},
		Getenv: func(key string) string { return w.vars[key] },
		Stdout: w.out,
		Stderr: w.err,
	}
	return w
}

func TestGenerateWritesTheWorkflowsAndCheckFindsThemCurrent(t *testing.T) {
	r := planFixture(t)
	os.MkdirAll(filepath.Join(r.git.Dir, ".github/workflows"), 0o755)
	os.MkdirAll(filepath.Join(r.git.Dir, "liken/flux"), 0o755)
	os.WriteFile(filepath.Join(r.git.Dir, fluxVersionFile), []byte(testFlux+"\n"), 0o644)
	os.MkdirAll(filepath.Join(r.git.Dir, "toolchains"), 0o755)
	os.WriteFile(filepath.Join(r.git.Dir, imagesFile), []byte(fixtureBuildKitImages), 0o644)
	w := newFakeEnv(t, &fakeRegistry{})
	if err := w.run([]string{"generate", "-root", r.git.Dir, "-check"}); err == nil {
		t.Fatal("the check passed before the workflows existed")
	}
	if err := w.run([]string{"generate", "-root", r.git.Dir}); err != nil {
		t.Fatalf("running the generate command: %v", err)
	}
	if err := w.run([]string{"generate", "-root", r.git.Dir, "-check"}); err != nil {
		t.Fatalf("running the generate command: %v", err)
	}
}

func TestSitesListsEachManualAndItsPrefix(t *testing.T) {
	r := planFixture(t)
	w := newFakeEnv(t, &fakeRegistry{})
	if err := w.run([]string{"sites", "-root", r.git.Dir}); err != nil {
		t.Fatalf("running the sites command: %v", err)
	}
	if w.out.String() != "operator:operator\n" {
		t.Errorf("sites printed %q, want the prefix of each site", w.out.String())
	}
}

func TestACommandNeedsAName(t *testing.T) {
	w := newFakeEnv(t, &fakeRegistry{})
	for _, args := range [][]string{nil, {"bake"}, {"publish", "-root", planFixture(t).git.Dir, "-component", "nope"}} {
		if err := w.run(args); err == nil {
			t.Errorf("%v ran", args)
		}
	}
}

func TestDepsPassesATreeWithNothingToCheck(t *testing.T) {
	root := writeTree(t, map[string]string{"a/package.toml": "[package]\nname = \"a\"\n"})
	w := newFakeEnv(t, &fakeRegistry{})
	if err := w.run([]string{"deps", "-root", root}); err != nil {
		t.Fatalf("running the deps command: %v", err)
	}
}

// runPlan runs the plan command on the fixture repository for a push
// to a branch. It returns the fake environment, the error, and the text that the
// command wrote to GITHUB_OUTPUT and GITHUB_STEP_SUMMARY.
func runPlan(t *testing.T, writers map[string]bool, env map[string]string) (*fakeEnv, error, string, string) {
	t.Helper()
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("operator/main.go", "package main // changed\n")
	r.commit("change the operator")
	w := newFakeEnv(t, &fakeRegistry{
		tags:    map[string][]string{"operator": {"2026.09.27-001"}, "base": {"2026.09.27-001"}},
		writers: writers,
	})
	dir := t.TempDir()
	output, summary := filepath.Join(dir, "output"), filepath.Join(dir, "summary")
	w.vars = map[string]string{
		"EVENT": "push", "REF": "refs/heads/topic", "BEFORE": before,
		"GITHUB_OUTPUT": output, "GITHUB_STEP_SUMMARY": summary,
	}
	for k, v := range env {
		w.vars[k] = v
	}
	err := w.run([]string{"plan", "-root", r.git.Dir})
	outputText, _ := os.ReadFile(output)
	summaryText, _ := os.ReadFile(summary)
	return w, err, string(outputText), string(summaryText)
}

func TestThePlanWritesItsDecisionsAndItsDryRun(t *testing.T) {
	_, err, output, summary := runPlan(t, map[string]bool{"token": true}, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true"})
	if err != nil {
		t.Fatalf("running the plan command: %v", err)
	}
	var decisions map[string]Decision
	line, _, _ := strings.Cut(output, "\n")
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "components=")), &decisions); err != nil {
		t.Fatalf("reading the components line of the output: %v: %s", err, output)
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishNone || !reflect.DeepEqual(d.Jobs, []string{"check", "codeql-check"}) ||
		!reflect.DeepEqual(d.Images, []ImageRun{{Image: "operator", Load: true}}) {
		t.Errorf("operator: %+v", d)
	}
	if !strings.Contains(output, `"base":{"check":false,"publish":"none","version":"","jobs":[],"images":[],"dryrun":false}`) {
		t.Errorf("a component that runs nothing lacks empty lists: %s", output)
	}
	for _, want := range []string{
		"Publishing is off",
		"| `operator` | check, codeql-check, operator | none | `-` | changed: operator/main.go |",
		"| `base` | skip | none | `-` | nothing in its paths changed |",
		"| `operator` | `2026.09.27-001` | yes | releases: changed: operator/main.go (since operator/2026.09.27-001) | `operator`, `operator-deploy` |",
		"| `liken` | `2026.09.28-001` | no | no change since 2026.09.28-001 | - |",
		"This workflow can push to every package.",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestThePlanWritesWhetherTheSiteDeploys(t *testing.T) {
	cases := []struct {
		name, ref, output, summary string
	}{
		{"a push to a branch", "refs/heads/topic", "site=false\n", "The site does not deploy: only a push to main deploys the site."},
		{"a push to main with no run that passed", "refs/heads/main", "site=true\n", "The site deploys: every job runs."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err, output, summary := runPlan(t, nil, map[string]string{"REF": c.ref, "GITHUB_TOKEN": "token",
				"GITHUB_REPOSITORY": "liken-sh/liken", "GITHUB_API_URL": actionsAPI(t, http.StatusOK, `{"workflow_runs":[]}`)})
			if err != nil {
				t.Fatalf("running the plan command: %v", err)
			}
			if !strings.Contains(output, c.output) || !strings.Contains(summary, c.summary) {
				t.Errorf("the output:\n%s\nthe summary:\n%s", output, summary)
			}
		})
	}
}

func TestThePlanListsEachPackageItCannotPushTo(t *testing.T) {
	_, err, _, summary := runPlan(t, nil, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true"})
	if err != nil {
		t.Fatalf("running the plan command: %v", err)
	}
	for _, want := range []string{"cannot push to 3 packages", "- operator: the registry refused an upload to operator: 403 Forbidden"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestThePlanFailsWhenItWouldPushToAPackageThatRefuses(t *testing.T) {
	_, err, _, _ := runPlan(t, nil, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true", "PUBLISH": "true"})
	if err == nil || !strings.Contains(err.Error(), "cannot push to 3 packages") {
		t.Errorf("the plan returned %v, want an error about pushing to 3 packages", err)
	}
}

func TestThePlanSkipsTheProbeWithoutAToken(t *testing.T) {
	_, err, _, summary := runPlan(t, nil, map[string]string{"PROBE": "true"})
	if err != nil {
		t.Fatalf("running the plan command: %v", err)
	}
	if !strings.Contains(summary, "Not checked") {
		t.Errorf("the summary:\n%s", summary)
	}
}

func TestTheRecordCreatesTheTagsRelease(t *testing.T) {
	r := planFixture(t)
	r.commit("an operator change")
	r.run("tag", "2026.10.02-001")
	w := newFakeEnv(t, &fakeRegistry{tags: map[string][]string{"operator": {"2026.10.02-001"}}})
	if err := w.run([]string{"record", "-root", r.git.Dir, "-tag", "2026.10.02-001"}); err != nil {
		t.Fatalf("running the record command: %v", err)
	}
	if len(w.recorded.commands) != 2 || !strings.HasPrefix(w.recorded.commands[1], "gh release edit 2026.10.02-001 --notes-file ") {
		t.Errorf("the commands run are %v", w.recorded.commands)
	}
	for _, want := range []string{"| `operator` | `2026.10.02-001` | released by this tag |", "| `liken` | `2026.09.28-001` | unchanged |", "- an operator change\n"} {
		if !strings.Contains(w.out.String(), want) {
			t.Errorf("the notes lack %q:\n%s", want, w.out.String())
		}
	}
	if strings.Contains(w.out.String(), "- an OS release") {
		t.Errorf("the notes list the commit \"an OS release\" from before the previous tag, want only the commits since it:\n%s", w.out.String())
	}
}

func TestThePublishCommandPushesSignsAndMovesLatest(t *testing.T) {
	r := planFixture(t)
	r.write(imagesFile, fixtureImages)
	w := newFakeEnv(t, &fakeRegistry{})
	w.vars["DOCKER_CONFIG"] = fixtureDockerConfig
	if err := w.run([]string{"publish", "-root", r.git.Dir, "-component", "base", "-version", "2026.10.02-001", "-mode", "release"}); err != nil {
		t.Fatalf("running the publish command: %v", err)
	}
	// The push, its signature, and the move of :latest.
	if len(w.recorded.commands) != 3 || w.recorded.commands[1] != signCommand("ghcr.io/liken-sh/base@"+fakeDigest("base")) {
		t.Errorf("the commands run are %v", w.recorded.commands)
	}
}

func TestTheOSVersionsComeFromTheChannelAndADeployArtifactFallsBackToItsImage(t *testing.T) {
	r := planFixture(t)
	components, err := LoadComponents(r.git.Dir)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	w := newFakeEnv(t, &fakeRegistry{tags: map[string][]string{"operator": {"2026.09.27-001"}, "operator-deploy": {"buildcache"}}})
	cases := map[string][]string{"liken": {"2026.09.28-001"}, "operator": {"2026.09.27-001"}, "brand": nil}
	for name, want := range cases {
		got, err := w.Published.Versions(components[name])
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
}

func TestThePlanOnMainComparesWithTheNewestSuccessfulRun(t *testing.T) {
	cases := []struct {
		name, want, body string
		status           int
	}{
		{"a run that passed", "the newest main run that passed", `{"workflow_runs":[{"head_sha":"GREEN"}]}`, http.StatusOK},
		{"an API that refuses", "Every job runs: the newest main run that passed is unknown", `{}`, http.StatusForbidden},
		{"no run that passed", "Every job runs: no main run has passed yet", `{"workflow_runs":[]}`, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := planFixture(t)
			green := r.run("rev-parse", "HEAD")
			r.write("operator/main.go", "package main // changed\n")
			before := r.commit("change the operator")
			r.write("liken/init/main.go", "package main\n")
			r.commit("change the OS")
			w := newFakeEnv(t, &fakeRegistry{})
			summary := filepath.Join(t.TempDir(), "summary")
			w.vars = map[string]string{
				"EVENT": "push", "REF": "refs/heads/main", "BEFORE": before, "GITHUB_STEP_SUMMARY": summary,
				"GITHUB_TOKEN": "token", "GITHUB_REPOSITORY": "liken-sh/liken",
				"GITHUB_API_URL": actionsAPI(t, c.status, strings.ReplaceAll(c.body, "GREEN", green)),
			}
			if err := w.run([]string{"plan", "-root", r.git.Dir}); err != nil {
				t.Fatalf("running the plan command: %v", err)
			}
			text, _ := os.ReadFile(summary)
			if !strings.Contains(string(text), c.want) {
				t.Errorf("the summary lacks %q:\n%s", c.want, text)
			}
		})
	}
}
