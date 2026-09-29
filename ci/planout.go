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
func (w World) plan(root string, components map[string]*Component) error {
	e := Event{
		Name:       w.Getenv("EVENT"),
		Ref:        w.Getenv("REF"),
		Before:     w.Getenv("BEFORE"),
		Base:       w.Getenv("BASE"),
		Head:       "HEAD",
		MainRef:    "origin/main",
		Publishing: w.Getenv("PUBLISH") == "true",
	}
	var notes []string
	if e.Main() {
		runs := Runs{API: orDefault(w.Getenv("GITHUB_API_URL"), "https://api.github.com"), Repository: w.Getenv("GITHUB_REPOSITORY"),
			Token: w.Getenv("GITHUB_TOKEN"), Client: w.Published.Client}
		verified, err := runs.NewestGreen("ci.yaml")
		if err != nil {
			notes = append(notes, fmt.Sprintf("The newest main run that passed is unknown, so the plan compares with the commit before the push alone: %v", err))
		}
		e.Verified = verified
	}
	git := Git{Dir: root}
	recipes, err := Recipes(root, components)
	if err != nil {
		return err
	}
	planner := Planner{Components: components, Git: git, Versions: onceEach(w.Published.Versions),
		Recipes: recipes, PublishedRecipe: w.Published.Recipe}
	decisions, err := planner.Plan(e)
	if err != nil {
		return err
	}
	// A tag's decisions are the release. Any other event also shows
	// what a release tag at this commit would publish, which is the dry
	// run of a release.
	candidates := decisions
	if e.Tag() == "" {
		if candidates, err = planner.Candidates(e.Head); err != nil {
			return err
		}
	}
	var refused []string
	probed := false
	if token := w.Getenv("GITHUB_TOKEN"); token != "" && w.Getenv("PROBE") == "true" {
		probed = true
		refused = probe(w.Registry(token), components)
	}

	var summary strings.Builder
	if e.Tag() == "" {
		list, all := planner.Comparisons(e)
		for _, c := range list {
			notes = append(notes, fmt.Sprintf("The plan compares with `%s`, %s.", c.Commit, c.Why))
		}
		if all != "" {
			notes = append(notes, "Every job runs: "+all+".")
		}
	}
	writeSummary(&summary, e, components, decisions, candidates, notes, probed, refused)
	fmt.Fprint(w.Stdout, summary.String())
	if path := w.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, summary.String()); err != nil {
			return err
		}
	}
	// A list that runs nothing is [] and never null, because each
	// workflow compares its input with '[]'.
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
	if path := w.Getenv("GITHUB_OUTPUT"); path != "" {
		if err := appendFile(path, "components="+string(encoded)+"\n"); err != nil {
			return err
		}
	}
	// A refused package fails the plan only when the run would push to
	// it. Before the repository allows publishing, the refusals are the
	// list of packages whose settings still need to grant this
	// repository write access.
	if len(refused) > 0 && e.Publishing {
		return fmt.Errorf("the workflow cannot push to %d packages", len(refused))
	}
	return nil
}

// probe proves that the token can push to every package of every
// component, and lists each refusal.
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

// onceEach asks for each component's versions once, because the plan
// and the dry run of a release both read them.
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

// imageNames lists the images that run.
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
