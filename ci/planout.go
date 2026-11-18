package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// plan reads the event from the environment that ci.yaml's plan job
// sets, decides what the run does with each component, and writes the
// decisions to the job's outputs and the run's summary.
func (e Env) plan(root string, components map[string]*Component) error {
	event := Event{
		Name:       e.Getenv("EVENT"),
		Ref:        e.Getenv("REF"),
		Before:     e.Getenv("BEFORE"),
		Base:       e.Getenv("BASE"),
		Head:       "HEAD",
		MainRef:    "origin/main",
		Publishing: e.Getenv("PUBLISH") == "true",
	}
	var notes []string
	// A push to main and a release tag both diff with the newest main
	// run that passed, so a job whose files did not change since that
	// run does not run again.
	if event.Main() || event.Tag() != "" {
		runs := Runs{API: orDefault(e.Getenv("GITHUB_API_URL"), "https://api.github.com"), Repository: e.Getenv("GITHUB_REPOSITORY"),
			Token: e.Getenv("GITHUB_TOKEN"), Client: e.Published.Client}
		verified, err := runs.NewestSuccessful("ci.yaml")
		switch {
		case err != nil:
			event.Unverified = fmt.Sprintf("the newest main run that passed is unknown (%v)", err)
		case verified == "":
			event.Unverified = "no main run has passed yet"
		}
		event.Verified = verified
	}
	git := Git{Dir: root}
	recipes, err := Recipes(root, components)
	if err != nil {
		return err
	}
	planner := Planner{Components: components, Git: git, Versions: onceEach(e.Published.Versions),
		Recipes: recipes, PublishedRecipe: e.Published.Recipe}
	decisions, err := planner.Plan(event)
	if err != nil {
		return err
	}
	// A tag's decisions are the release. Any other event also shows
	// what a release tag at this commit would publish, which is the dry
	// run of a release.
	candidates := decisions
	if event.Tag() == "" {
		if candidates, err = planner.Candidates(event.Head); err != nil {
			return err
		}
	}
	var refused []string
	probed := false
	if token := e.Getenv("GITHUB_TOKEN"); token != "" && e.Getenv("PROBE") == "true" {
		probed = true
		refused = probe(e.Registry(token), components)
	}

	list, all := planner.Comparisons(event)
	for _, c := range list {
		notes = append(notes, fmt.Sprintf("The plan compares with `%s`, %s.", c.Commit, c.Why))
	}
	if all != "" {
		notes = append(notes, "Every job runs: "+all+".")
	}
	site, siteWhy, err := planner.Site(event, decisions)
	if err != nil {
		return err
	}
	if site {
		notes = append(notes, "The site deploys: "+siteWhy+".")
	} else {
		notes = append(notes, "The site does not deploy: "+siteWhy+".")
	}
	actions, actionsWhy, err := planner.Actions(event)
	if err != nil {
		return err
	}
	if actions {
		notes = append(notes, "CodeQL analyzes the Actions code: "+actionsWhy+".")
	} else {
		notes = append(notes, "CodeQL does not analyze the Actions code: "+actionsWhy+".")
	}
	rescan, rescanWhy, err := planner.Rescan(event, decisions)
	if err != nil {
		return err
	}
	notes = append(notes, rescanNote(rescan, rescanWhy))
	var summary strings.Builder
	writeSummary(&summary, event, components, decisions, candidates, notes, probed, refused)
	fmt.Fprint(e.Stdout, summary.String())
	if path := e.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, summary.String()); err != nil {
			return err
		}
	}
	// An empty Jobs or Images encodes as [], not null, because the
	// component workflow declares its jobs and images inputs as JSON
	// lists.
	for name, d := range decisions {
		if d.Jobs == nil {
			d.Jobs = []string{}
		}
		if d.Images == nil {
			d.Images = []ImageRun{}
		}
		decisions[name] = d
	}
	encoded, err := json.Marshal(decisions)
	if err != nil {
		return err
	}
	if rescan == nil {
		rescan = []ScanRun{}
	}
	scans, err := json.Marshal(rescan)
	if err != nil {
		return err
	}
	if path := e.Getenv("GITHUB_OUTPUT"); path != "" {
		if err := appendFile(path, fmt.Sprintf("components=%s\nsite=%t\nactions=%t\ntrivy=%s\ntrivy-db=%t\n",
			encoded, site, actions, scans, ScansImages(components, decisions, rescan))); err != nil {
			return err
		}
	}
	// A refused package fails the plan only when the run would push to
	// it. Before the repository allows publishing, the refusals are the
	// list of packages whose settings still need to grant this
	// repository write access.
	if len(refused) > 0 && event.Publishing {
		return fmt.Errorf("the workflow cannot push to %d packages", len(refused))
	}
	return nil
}

// probe checks that the token can push to every package of every
// component, and returns each refusal.
func probe(registry Registry, components map[string]*Component) []string {
	var refused []string
	for _, c := range sortedComponents(components) {
		for _, pkg := range c.Packages() {
			if err := registry.CanPush(pkg); err != nil {
				refused = append(refused, fmt.Sprintf("%s: %v", pkg, err))
			}
		}
	}
	return refused
}

func writeSummary(b *strings.Builder, e Event, components map[string]*Component, decisions, candidates map[string]Decision, notes []string, probed bool, refused []string) {
	b.WriteString("## The plan\n\n")
	for _, note := range notes {
		b.WriteString(note + "\n\n")
	}
	if !e.Publishing {
		b.WriteString("Publishing is off: the repository variable `PUBLISH` is not `true`, so this run pushes nothing.\n\n")
	}
	b.WriteString("| Component | Jobs | Publishes | Version | Why |\n| --- | --- | --- | --- | --- |\n")
	for _, c := range sortedComponents(components) {
		d := decisions[c.Name()]
		jobs := "skip"
		if d.Check {
			jobs = strings.Join(append(slices.Clone(d.Jobs), imageNames(d.Images)...), ", ")
		}
		version := d.Version
		if version == "" || d.Publish == publishNone {
			version = "-"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | `%s` | %s |\n", c.Name(), jobs, d.Publish, version, d.Reason)
	}
	if e.Tag() == "" {
		b.WriteString("\n## A release tag at this commit\n\n")
		b.WriteString("| Component | Newest release | Releases | Why | Pushes |\n| --- | --- | --- | --- | --- |\n")
		for _, c := range sortedComponents(components) {
			if !c.HasOutputs() {
				continue
			}
			d := candidates[c.Name()]
			releases := "no"
			pushes := "-"
			if d.Changed {
				releases = "yes"
				pushes = strings.Join(outputsOf(c), ", ")
			}
			newest := d.Newest
			if newest == "" {
				newest = "none"
			}
			fmt.Fprintf(b, "| `%s` | `%s` | %s | %s | %s |\n", c.Name(), newest, releases, d.Reason, pushes)
		}
	}
	b.WriteString("\n## Write access\n\n")
	switch {
	case !probed:
		b.WriteString("Not checked: this event has no token that may push.\n")
	case len(refused) == 0:
		b.WriteString("This workflow can push to every package.\n")
	default:
		fmt.Fprintf(b, "This workflow cannot push to %d packages. In each package's settings, grant `liken-sh/liken` the Write role under Manage Actions access:\n\n", len(refused))
		for _, r := range refused {
			fmt.Fprintf(b, "- %s\n", r)
		}
	}
}

// outputsOf lists what a component pushes, for the summary.
func outputsOf(c *Component) []string {
	if c.Outputs.Channel {
		return []string{"the release channel"}
	}
	names := make([]string, 0)
	for _, pkg := range c.Packages() {
		names = append(names, "`"+pkg+"`")
	}
	return names
}

// onceEach caches the result of versions for each component, because
// the plan and the dry run of a release both read the versions, and
// each read queries the registry. A failed read is not cached.
func onceEach(versions VersionsFunc) VersionsFunc {
	seen := map[string][]string{}
	return func(c *Component) ([]string, error) {
		if v, ok := seen[c.Name()]; ok {
			return v, nil
		}
		v, err := versions(c)
		if err == nil {
			seen[c.Name()] = v
		}
		return v, err
	}
}

func imageNames(runs []ImageRun) []string {
	var names []string
	for _, run := range runs {
		names = append(names, run.Image)
	}
	return names
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
