package main

import (
	"reflect"
	"strings"
	"testing"
)

// rescanFixture is a repository with the ci component and its Trivy
// configuration, a pinned base with one image, and a tracked operator
// on the base with two images and a deploy artifact. The release
// 2026.09.27-001 is tagged.
func rescanFixture(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, map[string]string{
		"ci/package.toml":         "[package]\nname = \"ci\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n",
		"ci/trivy.go":             "package main\n",
		"ci/templates/trivy.tmpl": "{% define \"trivy-setup\" %}{% end %}\n",
		"ci/publish.go":           "package main\n",
		"base/package.toml":       "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"base/Dockerfile":         "FROM debian@sha256:aaa\n",
		"operator/package.toml":   "[package]\nname = \"operator\"\n[depends]\ncomponents = [\"base\"]\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\ndescription = \"The operator image.\"\n[[outputs.images]]\nname = \"operator-cli\"\ndescription = \"The operator-cli image.\"\n",
		"operator/Dockerfile":     "FROM base\n",
		"operator/main.go":        "package main\n",
	})
	r.run("tag", "2026.09.27-001")
	return r
}

// rescanPlanner plans the fixture with the operator's published
// versions, and with the base's tag published when base is true.
func (r *repo) rescanPlanner(operator []string, base bool) Planner {
	r.t.Helper()
	p := r.planner(map[string][]string{"operator": operator})
	recipes, err := Recipes(r.git.Dir, p.Components)
	if err != nil {
		r.t.Fatal(err)
	}
	p.Recipes = recipes
	p.PublishedRecipe = func(c *Component) (string, bool, error) { return recipes[c.Name()], base, nil }
	return p
}

// rescan changes the files, commits, and plans the push with the event,
// and returns what the trivy job scans and why.
func (r *repo) rescan(p Planner, e Event, files map[string]string) ([]ScanRun, string) {
	r.t.Helper()
	e.Name, e.Before, e.Head = "push", r.run("rev-parse", "HEAD"), "HEAD"
	for name, text := range files {
		r.write(name, text)
	}
	r.commit("the change")
	decisions, err := p.Plan(e)
	if err != nil {
		r.t.Fatal(err)
	}
	runs, why, err := p.Rescan(e, decisions)
	if err != nil {
		r.t.Fatal(err)
	}
	return runs, why
}

var operatorPublished = []string{"2026.09.27-001", "2026.09.27-001-dev-003-abcdef01", "latest"}

// everyImage is what the trivy job scans in the fixture when nothing
// publishes: the base at its tag, and each image of the operator at
// the operator's newest development build.
var everyImage = []ScanRun{
	{Image: "base", Version: "20260928-1"},
	{Image: "operator", Version: "2026.09.27-001-dev-003-abcdef01"},
	{Image: "operator-cli", Version: "2026.09.27-001-dev-003-abcdef01"},
}

// A change to the Trivy configuration scans every published image
// again on a push to main. A change to any other file, a push to a
// branch, and a test of the configuration scan nothing again.
func TestAPushToMainScansThePublishedImagesAgainWhenTheTrivyConfigurationChanges(t *testing.T) {
	main := Event{Ref: "refs/heads/main"}
	cases := []struct {
		name  string
		event Event
		file  string
		runs  []ScanRun
		why   string
	}{
		{"the Trivy version or a scan's arguments", main, "ci/trivy.go", everyImage, "changed: ci/trivy.go"},
		{"a step that installs Trivy or uploads a scan", main, "ci/templates/trivy.tmpl", everyImage, "changed: ci/templates/trivy.tmpl"},
		{"every job runs", Event{Ref: "refs/heads/main", Unverified: "the API answered 403"}, "operator/docs/index.md", everyImage, "every job runs"},
		{"the publish code", main, "ci/publish.go", nil, "no file of the Trivy configuration changed: ci/trivy.go, ci/templates/trivy.tmpl"},
		{"a test of the configuration", main, "ci/trivy_test.go", nil, "no file of the Trivy configuration changed: ci/trivy.go, ci/templates/trivy.tmpl"},
		{"an operator's source", main, "operator/main.go", nil, "no file of the Trivy configuration changed: ci/trivy.go, ci/templates/trivy.tmpl"},
		{"a push to a branch", Event{Ref: "refs/heads/topic"}, "ci/trivy.go", nil, "only a push to main scans the published images again"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := rescanFixture(t)
			runs, why := r.rescan(r.rescanPlanner(operatorPublished, true), c.event, map[string]string{c.file: "package main // changed\n"})
			if !reflect.DeepEqual(runs, c.runs) || why != c.why {
				t.Errorf("scans %v (%s), want %v (%s)", runs, why, c.runs, c.why)
			}
		})
	}
}

// A component that publishes in the run is scanned by its own publish,
// and a pinned tag that is not published yet has nothing to scan.
func TestTheRescanLeavesOutEachComponentThatPublishesInTheRun(t *testing.T) {
	cases := []struct {
		name       string
		publishing bool
		base       bool
		runs       []ScanRun
	}{
		{"the operator publishes a development build", true, true, everyImage[:1]},
		{"the base's tag is not published", false, false, everyImage[1:]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := rescanFixture(t)
			e := Event{Ref: "refs/heads/main", Publishing: c.publishing}
			runs, _ := r.rescan(r.rescanPlanner(operatorPublished, c.base), e, map[string]string{
				"ci/trivy.go":      "package main // changed\n",
				"operator/main.go": "package main // changed\n",
			})
			if !reflect.DeepEqual(runs, c.runs) {
				t.Errorf("scans %v, want %v", runs, c.runs)
			}
		})
	}
}

func TestARescanThatFindsNothingToScanSaysWhy(t *testing.T) {
	r := rescanFixture(t)
	runs, why := r.rescan(r.rescanPlanner(nil, false), Event{Ref: "refs/heads/main"}, map[string]string{"ci/trivy.go": "package main // changed\n"})
	note := rescanNote(runs, why)
	if want := "Trivy scans no published image again: changed: ci/trivy.go, and each image publishes in this run or has no published version."; note != want {
		t.Errorf("the note is %q", note)
	}
}

func TestTheRescanNoteNamesEachImageAndItsVersion(t *testing.T) {
	note := rescanNote(everyImage[:2], "changed: ci/trivy.go")
	want := "Trivy scans 2 published images again, with no build and no push (changed: ci/trivy.go): `base:20260928-1`, `operator:2026.09.27-001-dev-003-abcdef01`."
	if note != want {
		t.Errorf("the note is %q", note)
	}
}

// The plan writes the images that the trivy job scans again as a JSON
// list, which is [] when it scans none, and says why in the summary.
func TestThePlanWritesWhatTrivyScansAgain(t *testing.T) {
	_, err, output, summary := planWorld(t, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\ntrivy=[]\ntrivy-db=false\n") ||
		!strings.Contains(summary, "Trivy scans no published image again: only a push to main scans the published images again.") {
		t.Errorf("the output:\n%s\nthe summary:\n%s", output, summary)
	}
}

func TestTheNewestPublishedVersionIsAReleaseOrADevelopmentBuild(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{"a release alone", []string{"2026.09.27-001", "2026.09.26-001", "latest"}, "2026.09.27-001"},
		{"a development build after the newest release", []string{"2026.09.27-001", "2026.09.27-001-dev-003-abcdef01"}, "2026.09.27-001-dev-003-abcdef01"},
		{"a release after the newest development build", []string{"2026.09.27-001-dev-003-abcdef01", "2026.09.28-001"}, "2026.09.28-001"},
		{"a development build alone", []string{"2026.09.27-001-dev-003-abcdef01", "buildcache"}, "2026.09.27-001-dev-003-abcdef01"},
		{"no version", []string{"latest", "buildcache"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewestPublished(c.tags); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The trivy-db job downloads the database when a job of the run scans
// an image: the trivy job, or a publish that pushes images. A dry run,
// a publish of no image, and the OS's publish scan nothing.
func TestTheDatabaseDownloadsWhenTheRunScansAnImage(t *testing.T) {
	components, err := LoadComponents(writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/package.toml": "[package]\nname = \"liken\"\n[outputs]\nchannel = true\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	none := Decision{Publish: publishNone}
	cases := []struct {
		name      string
		decisions map[string]Decision
		rescan    []ScanRun
		want      bool
	}{
		{"a rescan", nil, everyImage[:1], true},
		{"a publish of an image", map[string]Decision{"base": {Publish: publishDev}}, nil, true},
		{"a dry run", map[string]Decision{"base": {Publish: publishNone, DryRun: true}}, nil, false},
		{"a publish of a deploy artifact alone", map[string]Decision{"base": none, "app": {Publish: publishRelease}}, nil, false},
		{"a publish of the OS", map[string]Decision{"base": none, "liken": {Publish: publishRelease}}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ScansImages(components, c.decisions, c.rescan); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
