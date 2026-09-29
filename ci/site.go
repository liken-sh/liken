package main

import (
	"slices"
	"strings"
)

// siteBuilder is the component whose sites and reports commands the
// root Makefile runs to build the site.
const siteBuilder = "ci"

// Site decides whether a push to main deploys the site. The site
// serves every manual and each component's coverage report, and its
// build reads nothing else: the root Makefile, and the commands of
// the ci program that list the manuals and the reports. A manual reads
// no file that its own hugo job does not read, and a report changes
// only when the job that writes its profile runs. So the site deploys
// when a hugo job or a job with a coverage profile runs, when a file
// that no component holds changes, when the ci program changes, when
// the site job itself changes, and when every job runs.
//
// A deploy that fails leaves the run red, so the next push compares
// with an older green run, and the same manual or report reaches the
// site on that push.
func (p Planner) Site(e Event, decisions map[string]Decision) (bool, string, error) {
	if !e.Main() {
		return false, "only a push to main deploys the site", nil
	}
	list, all := p.Comparisons(e)
	if all != "" {
		return true, "every job runs", nil
	}
	diff, err := p.readDiffs(list, e.Head)
	if err != nil {
		return false, "", err
	}
	if diff.Site {
		return true, "its job in " + rootWorkflowFile + " changed", nil
	}
	for _, file := range diff.Files {
		owner := ownerOf(p.Components, file)
		switch {
		case owner == "" && !strings.HasPrefix(file, plansDir):
			return true, "changed: " + file, nil
		case owner == siteBuilder && outputFile(p.Components[owner], []string{file}) != "":
			return true, "changed: " + file, nil
		}
	}
	for _, c := range sortedComponents(p.Components) {
		d := decisions[c.Name()]
		if !d.Check {
			continue
		}
		for _, job := range c.Jobs {
			if (job.Toolchain == "hugo" || len(job.Coverage) > 0) && slices.Contains(d.Jobs, job.Name) {
				return true, c.Name() + "'s " + job.Name + " job runs", nil
			}
		}
	}
	return false, "no manual, coverage report, or build of the site changed", nil
}
