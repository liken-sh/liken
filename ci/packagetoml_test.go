package main

import (
	"strings"
	"testing"
)

// The base's package.toml from planFixture, with its check's command and its
// excluded paths as given, so a test edits one part and keeps the rest.
func baseConfig(run, exclude string) string {
	return "[package]\nname = \"base\"\n[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"" + run + "\"\n" +
		"[outputs]\nexclude = [" + exclude + "]\n[[outputs.images]]\nname = \"base\"\n"
}

// A release decision for each component, for the commits after the fixture's
// releases.
func decided(t *testing.T, r *repo) map[string]Decision {
	t.Helper()
	decisions, err := r.planner(fixturePublished).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return decisions
}

// The fixture with the base's package.toml in the shape baseConfig writes,
// committed and released, so a test's own edit is the only change since.
func configFixture(t *testing.T) *repo {
	t.Helper()
	r := planFixture(t)
	r.write("base/.dockerignore", "notes/\n")
	r.write("base/notes/build.md", "how the base builds\n")
	r.write("base/package.toml", baseConfig("make test", ""))
	r.commit("the base excludes nothing")
	r.run("tag", "-f", "base/2026.09.27-001")
	r.run("tag", "-f", "operator/2026.09.27-001")
	return r
}

// The jobs of a package.toml run the component's checks, and the excluded
// paths name files no output is built from, so an edit to either changes no
// output, and releases neither the component nor the components that depend
// on it.
func TestAnEditToTheJobsOrTheExcludedPathsReleasesNothing(t *testing.T) {
	cases := map[string]string{
		"a job":            baseConfig("make check", ""),
		"an excluded path": baseConfig("make test", "\"notes/\""),
	}
	for name, config := range cases {
		r := configFixture(t)
		r.write("base/package.toml", config)
		r.commit("edit " + name)

		decisions := decided(t, r)
		for _, component := range []string{"base", "operator"} {
			if d := decisions[component]; d.Changed {
				t.Errorf("%s: %s releases: %+v", name, component, d)
			}
		}
	}
}

// An edit to any other part of a package.toml can change what a build makes,
// so it releases the component and every component that depends on it.
func TestAnEditToAnImageReleasesTheComponentAndItsUsers(t *testing.T) {
	r := configFixture(t)
	r.write("base/package.toml", baseConfig("make test", "")+"platforms = [\"linux/arm64\"]\n")
	r.commit("build the base for another platform")

	decisions := decided(t, r)
	if d := decisions["base"]; !d.Changed || !strings.HasPrefix(d.Why, "changed: base/package.toml") {
		t.Errorf("base: %+v", d)
	}
	if d := decisions["operator"]; !d.Changed || !strings.HasPrefix(d.Why, "its dependency base changed: base/package.toml") {
		t.Errorf("operator: %+v", d)
	}
}

// The jobs are what the checks run, so an edit to them still runs the
// component's own checks, and no check of a component that depends on it.
func TestAnEditToTheJobsRunsOnlyTheComponentsOwnChecks(t *testing.T) {
	r := configFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("base/package.toml", baseConfig("make check", ""))
	r.commit("edit the base's check")

	decisions := planPush(t, r, before)
	if !decisions["base"].Check || decisions["operator"].Check {
		t.Errorf("the checks that run: %v", checked(decisions))
	}
}

// A push to main publishes a development build of what changed, and an edit
// to the jobs changes no output, so it publishes nothing.
func TestAnEditToTheJobsOnMainPublishesNoDevelopmentBuild(t *testing.T) {
	r := configFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("base/package.toml", baseConfig("make check", ""))
	r.commit("edit the base's check")

	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"base", "operator"} {
		if d := decisions[component]; d.Publish != publishNone {
			t.Errorf("%s: %+v", component, d)
		}
	}
}

// A package.toml with no outputs table that gains one only for its excluded
// paths, as brand's did, still changes no output.
func TestAnOutputsTableThatHoldsOnlyExcludedPathsReleasesNothing(t *testing.T) {
	r := planFixture(t)
	r.write("brand/voice.md", "the rules\n")
	r.commit("the voice rules")
	r.run("tag", "-f", "2026.09.28-001")
	r.write("brand/package.toml", "[package]\nname = \"brand\"\n[outputs]\nexclude = [\"voice.md\"]\n"+
		"[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"make test\"\n")
	r.commit("brand's prose goes into no build")

	if d := decided(t, r)["liken"]; d.Changed {
		t.Errorf("liken releases: %+v", d)
	}
}

// A push to main compares with its own before and with the newest main run
// that passed. When a run in between changed the base's image and failed,
// the push's own change is only to the jobs, but the change since the green
// run is to the image, so the dependent's checks still run.
func TestAJobsEditAfterAnUnverifiedImageChangeStillRunsTheDependents(t *testing.T) {
	r := configFixture(t)
	green := r.run("rev-parse", "HEAD")
	r.write("base/package.toml", baseConfig("make test", "")+"platforms = [\"linux/arm64\"]\n")
	before := r.commit("build the base for another platform, in a run that failed")
	r.write("base/package.toml", baseConfig("make check", "")+"platforms = [\"linux/arm64\"]\n")
	r.commit("edit the base's check")

	decisions := planGate(t, r, map[string][]string{}, Event{Name: "push", Ref: "refs/heads/main", Before: before, Verified: green, Head: "HEAD"})
	if !decisions["operator"].Check {
		t.Errorf("the checks that run: %v", checked(decisions))
	}
}
