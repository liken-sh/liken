package main

import (
	"reflect"
	"strings"
	"testing"
)

func planGate(t *testing.T, r *repo, published map[string][]string, e Event) map[string]Decision {
	t.Helper()
	decisions, err := r.planner(published).Plan(e)
	if err != nil {
		t.Fatal(err)
	}
	return decisions
}

// A main run that fails leaves its change unverified. The next push
// compares with the newest green run too, so the change runs again.
// When the plan cannot say which run was the newest green one, it runs
// every job: a change that it skipped would stay unverified for good,
// because the next green run would hide it.
func TestAPushToMainRunsWhatTheNewestGreenRunDidNotVerify(t *testing.T) {
	r := planFixture(t)
	root := r.run("rev-parse", "HEAD")
	r.run("checkout", "--quiet", "-b", "old")
	r.write("brand/voice.md", "a rule on a branch that main left behind\n")
	aside := r.commit("a green run's commit that main no longer holds")
	r.run("checkout", "--quiet", "main")
	r.write("operator/main.go", "package main // changed in a run that failed\n")
	before := r.commit("change the operator")
	r.write("liken/init/main.go", "package main\n")
	r.commit("change the OS")
	cases := []struct {
		name       string
		verified   string
		unverified string
		checks     []string
	}{
		{"with the newest green run", root, "", []string{"liken", "operator"}},
		{"with a green run that is not an ancestor, from the merge base", aside, "", []string{"liken", "operator"}},
		{"with a green run that is not in the repository", "1111111111111111111111111111111111111111", "", []string{"base", "brand", "liken", "operator"}},
		{"when the API failed", "", "the API answered 403", []string{"base", "brand", "liken", "operator"}},
		{"with no lookup, as on a workstation", "", "", []string{"liken"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decisions := planGate(t, r, map[string][]string{}, Event{Name: "push", Ref: "refs/heads/main", Before: before,
				Verified: c.verified, Unverified: c.unverified, Head: "HEAD"})
			if got := checked(decisions); !reflect.DeepEqual(got, c.checks) {
				t.Errorf("the checks that run: %v, want %v", got, c.checks)
			}
		})
	}
}

// A branch run that was cancelled leaves its change unchecked. The next
// push compares with the branch's merge base with main too, so the
// change runs again.
func TestABranchPushRunsWhatTheBranchChangedSinceMain(t *testing.T) {
	r, _, _ := branchFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("liken/init/main.go", "package main\n")
	r.commit("change the OS on the topic branch")
	decisions := planGate(t, r, fixturePublished, Event{Name: "push", Ref: "refs/heads/topic", Before: before, MainRef: "main", Head: "HEAD"})
	if got := checked(decisions); !reflect.DeepEqual(got, []string{"liken", "operator"}) {
		t.Errorf("the checks that run: %v", got)
	}
}

// devPublished is a registry's list of the operator's versions, with a
// development build of the commit.
func devPublished(commit string) map[string][]string {
	return map[string][]string{
		"operator": {"2026.09.27-001", "2026.09.28-001-dev-999-00000000", "2026.09.28-001-dev-1000-" + commit[:8]},
		"base":     {"2026.09.27-001", "2026.09.28-001-dev-001-" + commit[:8]},
	}
}

// A development build that did not publish is published by the next
// push to main, because the plan compares with the commit of the newest
// development build, not only with the commit before the push.
func TestAPushToMainPublishesWhatTheNewestDevelopmentBuildLacks(t *testing.T) {
	r := planFixture(t)
	published := r.run("rev-parse", "HEAD")
	r.write("operator/main.go", "package main // changed in a run that did not publish\n")
	before := r.commit("change the operator")
	r.write("liken/init/main.go", "package main\n")
	r.commit("change the OS")
	decisions := planGate(t, r, devPublished(published), Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	d := decisions["operator"]
	if !d.Check || d.Publish != publishDev || !strings.Contains(d.Reason, "operator/main.go") {
		t.Errorf("operator: %+v", d)
	}
	if d := decisions["base"]; d.Check || d.Publish != publishNone {
		t.Errorf("base: %+v", d)
	}
}

func TestAPushToMainPublishesNothingWhenTheNewestDevelopmentBuildIsCurrent(t *testing.T) {
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("liken/init/main.go", "package main\n")
	r.commit("change the OS")
	decisions := planGate(t, r, devPublished(before), Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	if d := decisions["operator"]; d.Check || d.Publish != publishNone {
		t.Errorf("operator: %+v", d)
	}
}

// branchFixture is planFixture with a topic branch that changes the
// operator, after which main changes the base.
func branchFixture(t *testing.T) (r *repo, branchPoint, mainTip string) {
	t.Helper()
	r = planFixture(t)
	branchPoint = r.run("rev-parse", "HEAD")
	r.run("checkout", "--quiet", "-b", "topic")
	r.write("operator/main.go", "package main // on the topic branch\n")
	r.commit("change the operator on the topic branch")
	r.run("checkout", "--quiet", "main")
	r.write("base/Dockerfile", "FROM scratch\nLABEL main=moved\n")
	mainTip = r.commit("change the base on main")
	r.run("checkout", "--quiet", "topic")
	return r, branchPoint, mainTip
}

func TestABranchRunsWhatItChangedFromWhereItLeftMain(t *testing.T) {
	cases := []struct {
		name  string
		event func(branchPoint, mainTip string) Event
	}{
		{"the first push of a new branch", func(string, string) Event {
			return Event{Name: "push", Ref: "refs/heads/topic", Before: "0000000000000000000000000000000000000000", MainRef: "main", Head: "HEAD"}
		}},
		{"a force push", func(_, mainTip string) Event {
			return Event{Name: "push", Ref: "refs/heads/topic", Before: mainTip, MainRef: "main", Head: "HEAD"}
		}},
		{"a pull request whose base moved on", func(_, mainTip string) Event {
			return Event{Name: "pull_request", Ref: "refs/pull/1/merge", Base: mainTip, Head: "HEAD"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, branchPoint, mainTip := branchFixture(t)
			decisions := planGate(t, r, fixturePublished, c.event(branchPoint, mainTip))
			if got := checked(decisions); !reflect.DeepEqual(got, []string{"operator"}) {
				t.Errorf("the checks that run: %v", got)
			}
		})
	}
}

func TestABranchWithNoMainToCompareWithRunsEveryJob(t *testing.T) {
	r, _, _ := branchFixture(t)
	decisions := planGate(t, r, fixturePublished, Event{Name: "push", Ref: "refs/heads/topic", Before: "0000000000000000000000000000000000000000", MainRef: "origin/main", Head: "HEAD"})
	if got := checked(decisions); len(got) != 4 {
		t.Errorf("the checks that run: %v", got)
	}
}

func TestTheNewestDevelopmentBuildSortsByItsCount(t *testing.T) {
	tags := []string{"2026.09.28-001", "2026.09.28-001-dev-999-aaaaaaaa", "2026.09.28-001-dev-1000-bbbbbbbb", "2026.09.27-003-dev-5000-cccccccc", "latest"}
	if got := NewestDev(tags); got != "2026.09.28-001-dev-1000-bbbbbbbb" {
		t.Errorf("NewestDev = %q", got)
	}
	if got := NewestDev([]string{"2026.09.28-001"}); got != "" {
		t.Errorf("NewestDev of releases alone = %q", got)
	}
}
