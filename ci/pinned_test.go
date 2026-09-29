package main

import (
	"strings"
	"testing"
)

// pinnedFixture is a repository with a pinned tool, a pinned base on
// the tool, and a tracked operator on the base. The operator's release
// 2026.09.28-001 is tagged.
func pinnedFixture(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, map[string]string{
		"tool/package.toml":     "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"tool\"\n",
		"tool/Dockerfile":       "FROM debian@sha256:aaa\n",
		"base/package.toml":     "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n[[jobs]]\nname = \"prek\"\ntoolchain = \"prek\"\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":       "FROM tool\n",
		"operator/package.toml": "[package]\nname = \"operator\"\n[depends]\ncomponents = [\"base\"]\n[[outputs.images]]\nname = \"operator\"\n",
		"operator/Dockerfile":   "FROM base\n",
	})
	r.run("tag", "2026.09.28-001")
	return r
}

// pinnedPlanner plans the fixture with a registry that holds each
// pinned tag in published, with the recipe the tree gives it or, for a
// tag in stale, another one.
func (r *repo) pinnedPlanner(published map[string]bool, stale map[string]bool) Planner {
	r.t.Helper()
	p := r.planner(map[string][]string{"operator": {"2026.09.28-001"}})
	recipes, err := Recipes(r.git.Dir, p.Components)
	if err != nil {
		r.t.Fatal(err)
	}
	p.Recipes = recipes
	p.PublishedRecipe = func(c *Component) (string, bool, error) {
		if stale[c.Name()] {
			return "sha256:old", true, nil
		}
		return recipes[c.Name()], published[c.Name()], nil
	}
	return p
}

func TestAPublishedPinnedTagWithItsRecipeBuildsNothing(t *testing.T) {
	r := pinnedFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("operator/main.go", "package main\n")
	r.commit("change the operator")
	e := Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true}
	decisions, err := r.pinnedPlanner(map[string]bool{"tool": true, "base": true}, nil).Plan(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tool", "base"} {
		if d := decisions[name]; d.Check || d.Publish != publishNone || d.Version != "20260928-1" {
			t.Errorf("%s: %+v", name, d)
		}
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishDev {
		t.Errorf("operator: %+v", d)
	}
}

func TestAPublishedPinnedTagRunsItsJobsWhenItsPathsChange(t *testing.T) {
	r := pinnedFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("base/README.md", "why\n")
	r.commit("explain the base")
	e := Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true}
	decisions, err := r.pinnedPlanner(map[string]bool{"tool": true, "base": true}, nil).Plan(e)
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["base"]; !d.Check || d.Publish != publishNone ||
		d.Reason != "changed: base/README.md; 20260928-1 is published, and its recipe matches" {
		t.Errorf("base: %+v", d)
	}
}

func TestAPinnedTagThatIsNotPublishedBuildsAndPublishes(t *testing.T) {
	r := pinnedFixture(t)
	published := map[string]bool{"tool": true}
	cases := map[string]struct {
		event   Event
		publish string
	}{
		"a pull request":  {Event{Name: "pull_request", Ref: "refs/pull/1/merge", Base: "HEAD", Head: "HEAD"}, publishNone},
		"a push to main":  {Event{Name: "push", Ref: "refs/heads/main", Before: "HEAD", Head: "HEAD", Publishing: true}, publishDev},
		"a release tag":   {Event{Name: "push", Ref: "refs/tags/2026.10.02-001", Head: "HEAD", Publishing: true}, publishRelease},
		"publishing off":  {Event{Name: "push", Ref: "refs/tags/2026.10.02-001", Head: "HEAD"}, publishNone},
		"a new branch":    {Event{Name: "push", Ref: "refs/heads/topic", Head: "HEAD", Publishing: true}, publishNone},
		"a dispatch":      {Event{Name: "workflow_dispatch", Ref: "refs/heads/main", Head: "HEAD"}, publishNone},
		"a push to topic": {Event{Name: "push", Ref: "refs/heads/topic", Before: "HEAD", Head: "HEAD", Publishing: true}, publishNone},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			decisions, err := r.pinnedPlanner(published, nil).Plan(c.event)
			if err != nil {
				t.Fatal(err)
			}
			if d := decisions["base"]; !d.Check || d.Publish != c.publish || d.Version != "20260928-1" || !strings.HasPrefix(d.Reason, "builds: 20260928-1 is not published yet") {
				t.Errorf("base: %+v", d)
			}
			if d := decisions["tool"]; d.Publish != publishNone || d.Version != "20260928-1" {
				t.Errorf("tool: %+v", d)
			}
		})
	}
}

func TestARecipeThatChangedWithNoNewRevisionFailsThePlan(t *testing.T) {
	r := pinnedFixture(t)
	planner := r.pinnedPlanner(map[string]bool{"tool": true, "base": true}, map[string]bool{"tool": true})
	_, err := planner.Plan(Event{Name: "push", Ref: "refs/heads/topic", Before: "HEAD", Head: "HEAD"})
	want := "tool: ghcr.io/liken-sh/tool:20260928-1 was built from the recipe \"sha256:old\", and the tree gives \"" +
		planner.Recipes["tool"] + "\". Raise revision in tool/package.toml, and in each pinned component that builds on it: base."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v", err)
	}
	if _, err := planner.Candidates("HEAD"); err == nil {
		t.Error("the dry run of a release passed")
	}
}

func TestTheRecordSaysWhatHappenedToEachPinnedTag(t *testing.T) {
	r := pinnedFixture(t)
	r.write("app/package.toml", "[package]\nname = \"app\"\nversion = \"20260928\"\nrevision = 4\n[[outputs.images]]\nname = \"app\"\n")
	r.write("app/Dockerfile", "FROM scratch\n")
	r.write("new/package.toml", "[package]\nname = \"new\"\nversion = \"20261001\"\nrevision = 1\n[[outputs.images]]\nname = \"new\"\n")
	r.write("new/Dockerfile", "FROM scratch\n")
	r.commit("add two pinned images")
	p := r.pinnedPlanner(nil, nil)
	states := map[string]struct {
		published bool
		previous  string
	}{
		"tool": {true, "20260928-0"},
		"base": {true, "20260928-1"},
		"app":  {false, ""},
		"new":  {true, ""},
	}
	pinned := func(c *Component) (bool, string, error) {
		s := states[c.Name()]
		return s.published, s.previous, nil
	}
	entries, err := Record(p.Components, p.Versions, pinned, "2026.10.02-001")
	if err != nil {
		t.Fatal(err)
	}
	notes := RecordNotes("2026.10.02-001", entries, "", nil)
	for _, want := range []string{
		"| `tool` | `20260928-1` | pinned, released by this tag |",
		"| `base` | `20260928-1` | pinned, unchanged |",
		"| `app` | `20260928-4` | pinned, not published yet |",
		"| `new` | `20261001-1` | pinned, released by this tag |",
		"| `operator` | `2026.09.28-001` | unchanged |",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes lack %q:\n%s", want, notes)
		}
	}
}

func TestThePinnedTagAtAnEarlierReleaseIsReadFromItsCommit(t *testing.T) {
	r := pinnedFixture(t)
	r.write("tool/package.toml", "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"tool\"\n")
	r.write("app/package.toml", "[package]\nname = \"app\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"app\"\n")
	r.write("app/Dockerfile", "FROM scratch\n")
	r.commit("raise the tool and add an app")
	components, err := LoadComponents(r.git.Dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"tool": "20260928-1", "base": "20260928-1", "app": ""}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := r.git.PinnedTagAt("2026.09.28-001", components[name])
			if err != nil || got != want {
				t.Errorf("got %q, %v, want %q", got, err, want)
			}
		})
	}
}
