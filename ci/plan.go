package main

import (
	"fmt"
	"slices"
	"strings"
)

// An Event is the push, pull request, or dispatch that started the
// workflow run.
type Event struct {
	// Name is the GitHub event name: push, pull_request, or
	// workflow_dispatch.
	Name string
	// Ref is the full ref: refs/heads/main or refs/tags/2026.10.02-001.
	Ref string
	// Before is the branch's commit before the push.
	Before string
	Base   string
	// Head is the commit the run builds.
	Head string
	// Verified is the head of the newest run on main that passed.
	// Unverified is the reason the plan could not find that run, such
	// as an error from the API or no run that passed yet. Both are ""
	// when the plan did not search for the run, as in a test or on a
	// workstation.
	Verified, Unverified string
	// MainRef is the ref of main in the checkout, such as origin/main.
	// A push to another branch is also compared with its merge base on
	// main.
	MainRef string
	// Publishing is true when the repository allows real publishes.
	// Without it, a push to main and a release tag run their checks
	// and push nothing.
	Publishing bool
}

// Tag is the release tag the event pushed, or "".
func (e Event) Tag() string {
	if e.Name != "push" {
		return ""
	}
	tag, _ := strings.CutPrefix(e.Ref, "refs/tags/")
	if tag == e.Ref {
		return ""
	}
	return tag
}

func (e Event) Main() bool { return e.Name == "push" && e.Ref == "refs/heads/main" }

// Publish modes for a component's workflow.
const (
	publishNone    = "none"
	publishDev     = "dev"
	publishRelease = "release"
	// publishDry builds what a publish would push, and pushes nothing.
	publishDry = "dry"
)

// publishCode lists the files of the publish of every component except
// the OS. When one of them changes, dryRun runs the publish job of each
// component in dry mode, which builds each image and pushes and signs
// nothing.
var publishCode = []string{"ci/publish.go", "ci/imagemeta.go", "ci/sign.go"}

// A Decision is what the run does with one component.
type Decision struct {
	// Check is true when the component's jobs run.
	Check bool `json:"check"`
	// Publish is none, dev, or release.
	Publish string `json:"publish"`
	// Version is the version the component's outputs get in this run.
	Version string `json:"version"`
	// Reason is why the component's jobs run, or why they do not.
	Reason string `json:"-"`
	// Changed is true when the change modifies a file that the
	// component's outputs are built from, so a release or a development
	// build would publish the component.
	Changed bool `json:"-"`
	// Why is the file or the reason that made Changed true.
	Why    string `json:"-"`
	Newest string `json:"-"`
	// Jobs and Images are the jobs and the images that run when Check
	// is true.
	Jobs   []string   `json:"jobs"`
	Images []ImageRun `json:"images"`
	// DryRun is true when the check stage runs the publish job in dry
	// mode, which builds each image and pushes nothing. The method
	// dryRun sets it.
	DryRun bool `json:"dryrun"`
}

type VersionsFunc func(*Component) ([]string, error)

// Planner decides what a run does with every component.
type Planner struct {
	Components map[string]*Component
	Git        Git
	Versions   VersionsFunc
	// Recipes is the recipe hash of each pinned component in the tree,
	// and PublishedRecipe reads the hash of its published tag.
	Recipes         map[string]string
	PublishedRecipe PublishedRecipeFunc
}

// Plan decides, for each component, whether its jobs run, whether it
// publishes, and under which version.
func (p Planner) Plan(e Event) (map[string]Decision, error) {
	if tag := e.Tag(); tag != "" {
		return p.release(e, tag)
	}
	return p.gate(e)
}

// release plans a release tag. A component releases when a change
// went into its outputs since its newest published release. A
// component that does not release runs nothing.
//
// A component that releases runs only the jobs and images that the
// change since the newest main run that passed touches, because its
// other jobs passed on the same files in that run. When the run
// publishes, it also builds each image that uses the VERSION build
// argument. When the plan cannot find that run, every job of the
// component runs.
func (p Planner) release(e Event, tag string) (map[string]Decision, error) {
	if err := CheckReleaseTag(tag); err != nil {
		return nil, err
	}
	decisions, err := p.Candidates(e.Head)
	if err != nil {
		return nil, err
	}
	list, all := p.Comparisons(e)
	diff, err := p.readDiffs(list, e.Head)
	if err != nil {
		return nil, err
	}
	sel := selector{root: p.Git.Dir, components: p.Components, producer: ImageProducers(p.Components), diff: diff}
	for name, d := range decisions {
		c := p.Components[name]
		if !c.Pinned() {
			d.Version = tag
		}
		if d.Changed {
			d.Check = true
			if e.Publishing {
				d.Publish = publishRelease
			}
			p.selectJobs(c, &d, sel, all != "")
		}
		decisions[name] = d
	}
	return decisions, nil
}

// Candidates returns a decision for each component: whether a release
// tag at the commit would publish it, and why.
func (p Planner) Candidates(head string) (map[string]Decision, error) {
	published, err := p.pinnedPublished()
	if err != nil {
		return nil, err
	}
	decisions := map[string]Decision{}
	for _, c := range sortedComponents(p.Components) {
		if c.Pinned() {
			decisions[c.Name()] = pinnedDecision(c, published[c.Name()], false, "")
			continue
		}
		d := Decision{Publish: publishNone}
		if !c.HasOutputs() {
			d.Reason = "publishes nothing"
			decisions[c.Name()] = d
			continue
		}
		if err := p.sinceRelease(c, head, &d); err != nil {
			return nil, err
		}
		if d.Changed {
			d.Reason = "releases: " + d.Why
		} else {
			d.Reason = "no change since " + d.Newest
		}
		decisions[c.Name()] = d
	}
	return decisions, nil
}

// sinceRelease sets d.Changed and d.Why: whether the component's
// outputs changed after its newest published release.
//
// The diff starts at the git tag of that release. That tag is the bare
// version for a release made in this repository, or
// <component>/<version> for a release that the component made in its
// own repository before it moved here. The prefixed tag is tried
// first, because a bare tag with the same date may be the OS's
// release and would point at the OS's commit. A component with no
// release, or whose release has no git tag here, counts as changed.
func (p Planner) sinceRelease(c *Component, head string, d *Decision) error {
	versions, err := p.Versions(c)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name(), err)
	}
	d.Newest = NewestRelease(versions)
	if d.Newest == "" {
		d.Changed, d.Why = true, "nothing is published yet"
		return nil
	}
	from := ""
	for _, tag := range []string{c.Name() + "/" + d.Newest, d.Newest} {
		if p.Git.HasTag(tag) {
			from = tag
			break
		}
	}
	if from == "" {
		d.Changed, d.Why = true, "no git tag marks its release "+d.Newest
		return nil
	}
	diff, err := p.Git.ReadDiff(p.Components, from, head)
	if err != nil {
		return err
	}
	d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), diff)
	if d.Changed {
		d.Why += " (since " + from + ")"
	}
	return nil
}

// gate plans a push to a branch, a pull request, or a dispatch. The
// jobs of every component whose check paths changed run. A push to
// main also publishes a development build of each component whose
// outputs changed since its newest published version, and runs that
// component's jobs before the publish.
func (p Planner) gate(e Event) (map[string]Decision, error) {
	list, all := p.Comparisons(e)
	diff, err := p.readDiffs(list, e.Head)
	if err != nil {
		return nil, err
	}
	var dev string
	if e.Main() {
		commit, err := p.Git.Commit(e.Head)
		if err != nil {
			return nil, err
		}
		tag, count, err := p.Git.Describe(commit)
		if err != nil {
			return nil, err
		}
		dev = DevVersion(tag, count, commit)
	}
	published, err := p.pinnedPublished()
	if err != nil {
		return nil, err
	}
	sel := selector{root: p.Git.Dir, components: p.Components, producer: ImageProducers(p.Components), diff: diff}
	decisions := map[string]Decision{}
	for _, c := range sortedComponents(p.Components) {
		d := Decision{Publish: publishNone, Version: dev}
		switch {
		case all != "":
			d.Check, d.Reason = true, "every job runs: "+all
			d.Changed, d.Why = true, d.Reason
		default:
			var file string
			if d.Check, file = CheckChanged(p.Components, c.Name(), diff); d.Check {
				d.Reason = "changed: " + file
			} else {
				d.Reason = "nothing in its paths changed"
			}
			d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), diff)
		}
		if c.Pinned() {
			d = pinnedDecision(c, published[c.Name()], d.Check, d.Reason)
		}
		// The OS publishes no development build, because machines
		// install from the release channel, which has only releases.
		devBuild := e.Main() && c.HasOutputs() && !c.Outputs.Channel
		if devBuild && e.Publishing && !c.Pinned() && !d.Changed {
			if err := p.sincePublished(c, e.Head, &d); err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name(), err)
			}
			if d.Changed && !d.Check {
				d.Check, d.Reason = true, "publishes: "+d.Why
			}
		}
		if devBuild && e.Publishing && d.Changed {
			d.Publish = publishDev
		}
		if d.Check {
			p.selectJobs(c, &d, sel, all != "")
		}
		if err := p.dryRun(c, &d, diff, all != "", e.Head); err != nil {
			return nil, err
		}
		decisions[c.Name()] = d
	}
	return decisions, nil
}

// selectJobs sets the jobs and images that run. Every job and image
// runs when every job of the run runs, when the change touches the
// component's workflow, or when the component is pinned and its tag is
// not published. Otherwise the selector picks the ones that the change
// touches, and a component with none does not run.
//
// The publish waits for the jobs and images that run, and then builds
// every image of the component.
func (p Planner) selectJobs(c *Component, d *Decision, sel selector, all bool) {
	_, workflow := sel.diff.Workflows[c.Name()]
	if all || workflow || c.Pinned() && d.Changed {
		d.Jobs, d.Images = everything(c)
	} else {
		d.Jobs, d.Images = sel.Select(c, d.Publish != publishNone)
	}
	if c.Pinned() && !d.Changed {
		d.Images = pullPublished(d.Images)
	}
	if len(d.Jobs) == 0 && len(d.Images) == 0 {
		d.Check, d.Reason = false, "no job or image reads the change; "+d.Reason
	}
}

// pullPublished returns the image runs of a pinned component whose
// published tag has the tree's recipe. The recipe covers every input of
// the build, so a build from the tree would make the same image and
// only download from upstream again. So pullPublished drops each image
// build, and keeps only the images that have a smoke check and Load
// set, which the run pulls from the registry and smoke-checks.
func pullPublished(images []ImageRun) []ImageRun {
	var pulled []ImageRun
	for _, image := range images {
		if image.Smoke != "" && image.Load {
			image.Published, image.Cache = true, false
			pulled = append(pulled, image)
		}
	}
	return pulled
}

// dryRun runs the component's publish job in dry mode when the
// component has outputs, the run does not publish it, and one of these
// is true:
//
//   - every job runs
//   - a file of publishCode changed, and the component is not the OS
//   - the component's publish job in a workflow changed
//
// When the decision has no version, as on a branch other than main,
// the dry run gets a development version, because the publish accepts
// only a development or a release version. The OS gets none.
func (p Planner) dryRun(c *Component, d *Decision, diff Diff, all bool, head string) error {
	changed := diff.Publishes[c.Name()]
	for _, file := range publishCode {
		if slices.Contains(diff.Files, file) && !c.Outputs.Channel {
			changed = file
		}
	}
	if all {
		changed = "every job runs"
	}
	if changed == "" || d.Publish != publishNone || !c.HasOutputs() {
		return nil
	}
	d.DryRun, d.Check = true, true
	d.Reason += "; a dry run of the publish, because of " + changed
	if d.Version != "" || c.Outputs.Channel {
		return nil
	}
	commit, err := p.Git.Commit(head)
	if err != nil {
		return err
	}
	tag, count, err := p.Git.Describe(commit)
	if err != nil {
		return err
	}
	d.Version = DevVersion(tag, count, commit)
	return nil
}
