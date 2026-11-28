package main

import (
	"slices"
	"strings"
)

// siteBuilder is the component whose `ci sites` and `ci reports`
// commands the root Makefile runs to build the site.
const siteBuilder = "ci"

// Site decides whether a push to main deploys the site. The site has
// every manual and each component's coverage report, and the root
// Makefile builds it with the ci program. A manual changes only when
// its hugo job runs, and a report changes only when the job that
// writes its coverage profile runs. So the site deploys when one of
// these is true:
//
//   - every job runs
//   - the site job or the site-deploy job in the root workflow changed
//   - a file that no component owns changed, other than a file under
//     plans/
//   - a file of the ci component changed that notOutputs does not
//     name
//   - a hugo job or a job with a coverage profile runs
//
// A failed deploy fails the run, so the next push compares with an
// older run that passed and deploys the same change again.
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
