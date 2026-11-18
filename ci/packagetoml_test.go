package main

import (
	"strings"
	"testing"
)

// baseConfig returns the base's package.toml from planFixture with the
// given check command and excluded paths, so a test changes one part.
func baseConfig(run, exclude string) string {
	return "[package]\nname = \"base\"\n[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"" + run + "\"\n" +
		"[outputs]\nexclude = [" + exclude + "]\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n"
}

// decided returns the release decision of each component for the
// commits after the fixture's releases.
func decided(t *testing.T, r *repo) map[string]Decision {
	t.Helper()
	decisions, err := r.planner(fixturePublished).Candidates("HEAD")
	if err != nil {
		t.Fatalf("listing the candidates: %v", err)
	}
	return decisions
}

// configFixture is planFixture with the base's package.toml written by
// baseConfig, committed and tagged, so a test's own edit is the only
// change since the release.
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

// The jobs of a package.toml run the component's checks, and the
// excluded paths list files that no output is built from, so an edit to
// either releases neither the component nor its dependents.
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
				t.Errorf("after editing %s, %s is %+v, want no release", name, component, d)
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
		t.Errorf("base is %+v, want a release because base/package.toml changed", d)
	}
	if d := decisions["operator"]; !d.Changed || !strings.HasPrefix(d.Why, "its dependency base changed: base/package.toml") {
		t.Errorf("operator is %+v, want a release because its dependency base changed", d)
	}
}

// An edit to the jobs runs the component's own checks, and no check of
// a component that depends on it.
func TestAnEditToTheJobsRunsOnlyTheComponentsOwnChecks(t *testing.T) {
	r := configFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("base/package.toml", baseConfig("make check", ""))
	r.commit("edit the base's check")

	decisions := planPush(t, r, before)
	if !decisions["base"].Check || decisions["operator"].Check {
		t.Errorf("the checks run for %v, want base only", checked(decisions))
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
		t.Fatalf("planning the event: %v", err)
	}
	for _, component := range []string{"base", "operator"} {
		if d := decisions[component]; d.Publish != publishNone {
			t.Errorf("%s is %+v, want no publish", component, d)
		}
	}
}

// A package.toml with no outputs table that gains one only for its
// excluded paths, as brand's has, still changes no output.
func TestAnOutputsTableThatHoldsOnlyExcludedPathsReleasesNothing(t *testing.T) {
	r := planFixture(t)
	r.write("brand/voice.md", "the rules\n")
	r.commit("the voice rules")
	r.run("tag", "-f", "2026.09.28-001")
	r.write("brand/package.toml", "[package]\nname = \"brand\"\n[outputs]\nexclude = [\"voice.md\"]\n"+
		"[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"make test\"\n")
	r.commit("brand's prose goes into no build")

	if d := decided(t, r)["liken"]; d.Changed {
		t.Errorf("liken is %+v, want no release", d)
	}
}

// A push to main compares with its before commit and with the newest
// main run that passed. When a failed run in between changed the base's
// image, the push itself changes only the jobs, but the diff since the
// run that passed includes the image change, so the dependent's checks run.
func TestAJobsEditAfterAnUnverifiedImageChangeStillRunsTheDependents(t *testing.T) {
	r := configFixture(t)
	green := r.run("rev-parse", "HEAD")
	r.write("base/package.toml", baseConfig("make test", "")+"platforms = [\"linux/arm64\"]\n")
	before := r.commit("build the base for another platform, in a run that failed")
	r.write("base/package.toml", baseConfig("make check", "")+"platforms = [\"linux/arm64\"]\n")
	r.commit("edit the base's check")

	decisions := planGate(t, r, map[string][]string{}, Event{Name: "push", Ref: "refs/heads/main", Before: before, Verified: green, Head: "HEAD"})
	if !decisions["operator"].Check {
		t.Errorf("the checks run for %v, want the operator among them", checked(decisions))
	}
}
