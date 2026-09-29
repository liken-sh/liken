package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// channel serves a release channel's versions.yaml.
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

// world is a World of fixtures: a registry, a channel, an environment,
// and a recorder in place of the commands it would run.
type world struct {
	World
	env      map[string]string
	out      *bytes.Buffer
	recorded *recorder
}

func newWorld(t *testing.T, fake *fakeRegistry) *world {
	t.Helper()
	registry := fake.serve(t)
	w := &world{env: map[string]string{}, out: &bytes.Buffer{}, recorded: &recorder{}}
	w.World = World{
		Published: Published{Registry: registry, Channel: channel(t, fixtureVersions), Client: http.DefaultClient},
		Registry: func(token string) Registry {
			r := registry
			r.Token = token
			return r
		},
		Run:    w.recorded.run,
		Getenv: func(key string) string { return w.env[key] },
		Stdout: w.out,
	}
	return w
}

func TestGenerateWritesTheWorkflowsAndCheckFindsThemCurrent(t *testing.T) {
	r := planFixture(t)
	os.MkdirAll(filepath.Join(r.git.Dir, ".github/workflows"), 0o755)
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"generate", "-root", r.git.Dir, "-check"}); err == nil {
		t.Fatal("the check passed before the workflows existed")
	}
	if err := w.run([]string{"generate", "-root", r.git.Dir}); err != nil {
		t.Fatal(err)
	}
	if err := w.run([]string{"generate", "-root", r.git.Dir, "-check"}); err != nil {
		t.Fatal(err)
	}
}

func TestSitesListsEachManualAndItsPrefix(t *testing.T) {
	r := planFixture(t)
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"sites", "-root", r.git.Dir}); err != nil {
		t.Fatal(err)
	}
	if w.out.String() != "operator:operator\n" {
		t.Errorf("sites printed %q", w.out.String())
	}
}

func TestACommandNeedsAName(t *testing.T) {
	w := newWorld(t, &fakeRegistry{})
	for _, args := range [][]string{nil, {"bake"}, {"publish", "-root", planFixture(t).git.Dir, "-component", "nope"}} {
		if err := w.run(args); err == nil {
			t.Errorf("%v ran", args)
		}
	}
}

func TestDepsPassesATreeWithNothingToCheck(t *testing.T) {
	root := writeTree(t, map[string]string{"a/package.toml": "[package]\nname = \"a\"\n"})
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"deps", "-root", root}); err != nil {
		t.Fatal(err)
	}
}

// planWorld runs the plan command on the fixture repository for a push
// to a branch, and returns its world and the two files the workflow
// reads.
func planWorld(t *testing.T, writers map[string]bool, env map[string]string) (*world, error, string, string) {
	t.Helper()
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("operator/main.go", "package main // changed\n")
	r.commit("change the operator")
	w := newWorld(t, &fakeRegistry{
		tags:    map[string][]string{"operator": {"2026.09.27-001"}, "base": {"2026.09.27-001"}},
		writers: writers,
	})
	dir := t.TempDir()
	output, summary := filepath.Join(dir, "output"), filepath.Join(dir, "summary")
	w.env = map[string]string{
		"EVENT": "push", "REF": "refs/heads/topic", "BEFORE": before,
		"GITHUB_OUTPUT": output, "GITHUB_STEP_SUMMARY": summary,
	}
	for k, v := range env {
		w.env[k] = v
	}
	err := w.run([]string{"plan", "-root", r.git.Dir})
	outputText, _ := os.ReadFile(output)
	summaryText, _ := os.ReadFile(summary)
	return w, err, string(outputText), string(summaryText)
}

func TestThePlanWritesItsDecisionsAndItsDryRun(t *testing.T) {
	_, err, output, summary := planWorld(t, map[string]bool{"token": true}, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	var decisions map[string]Decision
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(output), "components=")), &decisions); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishNone {
		t.Errorf("operator: %+v", d)
	}
	for _, want := range []string{
		"Publishing is off",
		"| `operator` | run | none | `-` | changed: operator/main.go |",
		"| `operator` | `2026.09.27-001` | yes | releases: changed: operator/main.go (since operator/2026.09.27-001) | `operator`, `operator-deploy` |",
		"| `liken` | `2026.09.28-001` | no | no change since 2026.09.28-001 | - |",
		"This workflow can push to every package.",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestThePlanListsEachPackageItCannotPushTo(t *testing.T) {
	_, err, _, summary := planWorld(t, nil, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cannot push to 3 packages", "- operator: the registry refused an upload to operator: 403 Forbidden"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestThePlanFailsWhenItWouldPushToAPackageThatRefuses(t *testing.T) {
	_, err, _, _ := planWorld(t, nil, map[string]string{"GITHUB_TOKEN": "token", "PROBE": "true", "PUBLISH": "true"})
	if err == nil || !strings.Contains(err.Error(), "cannot push to 3 packages") {
		t.Errorf("got %v", err)
	}
}

func TestThePlanSkipsTheProbeWithoutAToken(t *testing.T) {
	_, err, _, summary := planWorld(t, nil, map[string]string{"PROBE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "Not checked") {
		t.Errorf("the summary:\n%s", summary)
	}
}

func TestTheRecordCreatesTheTagsRelease(t *testing.T) {
	r := planFixture(t)
	r.commit("an operator change")
	r.run("tag", "2026.10.02-001")
	w := newWorld(t, &fakeRegistry{tags: map[string][]string{"operator": {"2026.10.02-001"}}})
	if err := w.run([]string{"record", "-root", r.git.Dir, "-tag", "2026.10.02-001"}); err != nil {
		t.Fatal(err)
	}
	if len(w.recorded.commands) != 2 || !strings.HasPrefix(w.recorded.commands[1], "gh release edit 2026.10.02-001 --notes-file ") {
		t.Errorf("commands %v", w.recorded.commands)
	}
	for _, want := range []string{"| `operator` | `2026.10.02-001` | released by this tag |", "| `liken` | `2026.09.28-001` | unchanged |", "- an operator change\n"} {
		if !strings.Contains(w.out.String(), want) {
			t.Errorf("the notes lack %q:\n%s", want, w.out.String())
		}
	}
	if strings.Contains(w.out.String(), "- an OS release") {
		t.Errorf("the notes reach past the previous tag:\n%s", w.out.String())
	}
}

func TestPublishRunsThroughTheWorld(t *testing.T) {
	r := planFixture(t)
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"publish", "-root", r.git.Dir, "-component", "base", "-version", "2026.10.02-001", "-mode", "release"}); err != nil {
		t.Fatal(err)
	}
	if len(w.recorded.commands) != 2 {
		t.Errorf("commands %v", w.recorded.commands)
	}
}

func TestTheOSVersionsComeFromTheChannelAndADeployArtifactFallsBackToItsImage(t *testing.T) {
	r := planFixture(t)
	components, err := LoadComponents(r.git.Dir)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, &fakeRegistry{tags: map[string][]string{"operator": {"2026.09.27-001"}, "operator-deploy": {"buildcache"}}})
	cases := map[string][]string{"liken": {"2026.09.28-001"}, "operator": {"2026.09.27-001"}, "brand": nil}
	for name, want := range cases {
		got, err := w.Published.Versions(components[name])
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
}

func TestThePlanOnMainComparesWithTheNewestGreenRun(t *testing.T) {
	cases := []struct {
		name, want string
		status     int
	}{
		{"a green run", "the newest main run that passed", http.StatusOK},
		{"an API that refuses", "The newest main run that passed is unknown", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := planFixture(t)
			green := r.run("rev-parse", "HEAD")
			r.write("operator/main.go", "package main // changed\n")
			before := r.commit("change the operator")
			r.write("liken/init/main.go", "package main\n")
			r.commit("change the OS")
			w := newWorld(t, &fakeRegistry{})
			summary := filepath.Join(t.TempDir(), "summary")
			w.env = map[string]string{
				"EVENT": "push", "REF": "refs/heads/main", "BEFORE": before, "GITHUB_STEP_SUMMARY": summary,
				"GITHUB_TOKEN": "token", "GITHUB_REPOSITORY": "liken-sh/liken",
				"GITHUB_API_URL": actionsAPI(t, c.status, `{"workflow_runs":[{"head_sha":"`+green+`"}]}`),
			}
			if err := w.run([]string{"plan", "-root", r.git.Dir}); err != nil {
				t.Fatal(err)
			}
			text, _ := os.ReadFile(summary)
			if !strings.Contains(string(text), c.want) {
				t.Errorf("the summary lacks %q:\n%s", c.want, text)
			}
		})
	}
}
