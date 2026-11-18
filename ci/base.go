package main

import (
	"regexp"
	"strconv"
)

// A comparison is a commit that a run diffs against, with the reason.
type comparison struct {
	Commit, Why string
}

// Comparisons returns the commits that the run diffs against, or the
// reason that it has none, in which case every job runs. A dispatch has
// none.
//
//   - A push to main diffs against the commit before the push and
//     against the head of the newest main run that passed, so a change
//     that a failed run did not verify runs again. When that head is not
//     an ancestor, as after a force push, the diff starts at the merge
//     base of the two. When the plan could not find that run, every job
//     runs, because a change skipped now would stay unverified after the
//     next run that passed.
//   - A push to another branch diffs against the commit before the push,
//     when that commit is an ancestor, and against the branch's merge
//     base with main, so a change that a cancelled run did not check
//     runs again.
//   - A pull request diffs against its merge base with the base branch,
//     because the base branch's newer commits are not part of the change.
//   - A release tag diffs against the newest main run that passed in the
//     same way, and runs every job when no such run is known.
func (p Planner) Comparisons(e Event) ([]comparison, string) {
	switch e.Name {
	case "pull_request":
		if base, err := p.Git.MergeBase(e.Base, e.Head); err == nil {
			return []comparison{{base, "the pull request's merge base"}}, ""
		}
		return nil, "the pull request's base is missing"
	case "push":
		if e.Tag() != "" {
			return p.sinceVerified(e, nil)
		}
		var list []comparison
		if p.Git.HasCommit(e.Before) && p.Git.IsAncestor(e.Before, e.Head) {
			list = append(list, comparison{e.Before, "the commit before the push"})
		}
		if e.Main() {
			if len(list) == 0 {
				return nil, "the push has no earlier commit to compare with"
			}
			return p.sinceVerified(e, list)
		}
		if e.MainRef != "" {
			if base, err := p.Git.MergeBase(e.MainRef, e.Head); err == nil {
				list = append(list, comparison{base, "the branch's merge base with main"})
			}
		}
		if len(list) == 0 {
			return nil, "the push has no earlier commit to compare with"
		}
		return list, ""
	}
	return nil, "a dispatch runs everything"
}

// sinceVerified adds the newest main run that passed to the commits
// that a push to main or a release tag diffs against, as Comparisons
// describes. With no lookup at all, as on a workstation, a push to main
// diffs against list alone, and a release tag runs every job because
// it has no other commit to compare with.
func (p Planner) sinceVerified(e Event, list []comparison) ([]comparison, string) {
	if e.Unverified != "" {
		return nil, e.Unverified
	}
	if e.Verified == "" {
		if len(list) == 0 {
			return nil, "the release tag has no main run that passed to compare with"
		}
		return list, ""
	}
	if !p.Git.HasCommit(e.Verified) {
		return nil, "the newest main run that passed is at a commit that the repository does not have"
	}
	base, err := p.Git.MergeBase(e.Verified, e.Head)
	if err != nil {
		return nil, "the newest main run that passed shares no history with this commit"
	}
	why := "the newest main run that passed"
	if base != e.Verified {
		why = "the merge base of the newest main run that passed and this commit"
	}
	return append(list, comparison{base, why}), ""
}

// readDiffs reads the diff against each commit and joins them, so a
// component runs when any diff changes it. Checks marks a package.toml
// true, as a change that does not change the version of any output,
// only when every diff that includes the file marks it true, because
// each commit can have a different version of the file.
func (p Planner) readDiffs(list []comparison, head string) (Diff, error) {
	joined := Diff{Workflows: map[string]string{}, Publishes: map[string]string{}, Targets: map[string]bool{}, Checks: map[string]bool{}}
	for _, c := range list {
		d, err := p.Git.ReadDiff(p.Components, c.Commit, head)
		if err != nil {
			return joined, err
		}
		for file, checks := range d.Checks {
			previous, seen := joined.Checks[file]
			joined.Checks[file] = checks && (previous || !seen)
		}
		joined.Files = append(joined.Files, d.Files...)
		for k, v := range d.Workflows {
			joined.Workflows[k] = v
		}
		for k, v := range d.Publishes {
			joined.Publishes[k] = v
		}
		for k := range d.Targets {
			joined.Targets[k] = true
		}
		joined.SharedBake = joined.SharedBake || d.SharedBake
		joined.Site = joined.Site || d.Site
		if joined.GitHub == "" {
			joined.GitHub = d.GitHub
		}
	}
	return joined, nil
}

// devVersionParts splits a development version into its release tag,
// its count, and its commit.
var devVersionParts = regexp.MustCompile(`^([0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})-dev-([0-9]{3,})-([0-9a-f]{8})$`)

// NewestDev returns the newest development version among tags, or "".
// The count has no fixed width, so it compares as a number: dev-1000
// is newer than dev-999.
func NewestDev(tags []string) string {
	newest, newestTag, newestCount := "", "", -1
	for _, tag := range tags {
		m := devVersionParts.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		count, _ := strconv.Atoi(m[2])
		if m[1] > newestTag || m[1] == newestTag && count > newestCount {
			newest, newestTag, newestCount = tag, m[1], count
		}
	}
	return newest
}

// sincePublished sets d.Changed and d.Why: whether the component's
// outputs changed after its newest published version, a release or a
// development build.
// A development build's version includes its commit, so the diff starts
// there. When the newest version is a release, or the build's commit is
// not an ancestor of head, sinceRelease sets d.
func (p Planner) sincePublished(c *Component, head string, d *Decision) error {
	versions, err := p.Versions(c)
	if err != nil {
		return err
	}
	dev := NewestDev(versions)
	m := devVersionParts.FindStringSubmatch(dev)
	if m == nil || m[1] < NewestRelease(versions) {
		return p.sinceRelease(c, head, d)
	}
	commit, err := p.Git.Commit(m[3])
	if err != nil || !p.Git.IsAncestor(commit, head) {
		return p.sinceRelease(c, head, d)
	}
	diff, err := p.Git.ReadDiff(p.Components, commit, head)
	if err != nil {
		return err
	}
	d.Newest = dev
	d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), diff)
	if d.Changed {
		d.Why += " (since " + dev + ")"
	}
	return nil
}
