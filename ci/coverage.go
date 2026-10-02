package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A component's coverage report shows every language its tests cover,
// and each language's profile comes from one check job. A push to main
// runs only the jobs that its change reaches, so the site takes each
// profile from one of two places: the file that this run's job wrote,
// or the copy that the site serves from the run where the job last
// ran. The site publishes every profile beside the reports, at
// coverage/<component>/<file>, so each deploy carries the profiles of
// the jobs that did not run on to the next one.
//
// The served copy describes the code at this commit. A job that did
// not run reads no file that changed since the newest main run that
// passed. When a coverage job runs, the plan deploys the site, and a
// run whose deploy fails does not pass. So the site serves the profile
// that the job wrote in that run, or in an earlier run where it read
// the same files. A report renders its source files from this commit,
// and they are the files that the profile describes.
//
// A report renders only when every one of its profiles is at hand. A
// report without one of its languages would state a lower coverage
// with nothing to say why, so the site leaves the report out and names
// the job that it waits for. The profiles that are at hand publish all
// the same, so each job fills in its own profile the next time it runs.

// report is one component's coverage report: the directory whose
// Makefile renders it, its path in the site, and the file each of its
// jobs writes.
type report struct {
	component *Component
	path      string
	profiles  []profile
}

type profile struct{ job, file string }

// coverageReports lists the report of every component with a coverage
// job. A component with a manual serves its report beside the manual,
// at coverage.html. A component without one serves it at
// coverage/<name>.html.
func coverageReports(components map[string]*Component) []report {
	var list []report
	for _, c := range sortedComponents(components) {
		r := report{component: c, path: "coverage/" + c.Name() + ".html"}
		if c.Docs != nil {
			r.path = strings.TrimPrefix(c.Docs.Prefix+"/coverage.html", "/")
		}
		for _, job := range c.Jobs {
			for _, file := range job.Coverage {
				r.profiles = append(r.profiles, profile{job.Name, file})
			}
		}
		if len(r.profiles) > 0 {
			list = append(list, r)
		}
	}
	return list
}

// errNotServed means that the site serves no copy of a profile: its
// job has not run since the site began to publish profiles.
var errNotServed = errors.New("not served")

// gatherReports puts every profile at hand into the component's
// directory, where its coverage-report target reads it, and into
// dest, the site's tree. It prints the directory and the site path of
// each report that can render. A failure to read the site stops the
// deploy, because a deploy without a profile would lose it: the next
// deploy could not find it either. The failed deploy leaves the run
// red, so the next push to main runs the job again.
func gatherReports(root, site, dest string, client *http.Client, components map[string]*Component, stdout, stderr io.Writer) error {
	for _, r := range coverageReports(components) {
		c := r.component
		var missing []string
		for _, p := range r.profiles {
			local := filepath.Join(root, c.Dir, p.file)
			text, err := os.ReadFile(local)
			if errors.Is(err, os.ErrNotExist) {
				text, err = fetchProfile(client, site+"/coverage/"+c.Name()+"/"+p.file)
				if errors.Is(err, errNotServed) {
					missing = append(missing, fmt.Sprintf("the %s job did not run, and the site serves no %s from an earlier run", p.job, p.file))
					continue
				}
				if err == nil {
					err = os.WriteFile(local, text, 0o644)
				}
			}
			if err != nil {
				return fmt.Errorf("%s: %s: %w", c.Name(), p.file, err)
			}
			published := filepath.Join(dest, "coverage", c.Name(), p.file)
			if err := os.MkdirAll(filepath.Dir(published), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(published, text, 0o644); err != nil {
				return err
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(stderr, "%s: the site leaves out the coverage report: %s\n", c.Name(), strings.Join(missing, "; "))
			continue
		}
		fmt.Fprintf(stdout, "%s:%s\n", c.Dir, r.path)
	}
	return nil
}

func fetchProfile(client *http.Client, url string) ([]byte, error) {
	text, _, err := fetchServed(client, url)
	return text, err
}

// How many times a fetch asks, and how long it waits before the next ask.
// The site's CDN answers with a 5xx now and then, and one such answer
// failed a build. Any failure but a 404 is asked again, and a 404 is an
// answer: the site does not serve the file.
const fetchTries = 3

var fetchPause = 2 * time.Second

// fetchServed reads one file of the site, and the Last-Modified time
// that the site gives it.
func fetchServed(client *http.Client, url string) (text []byte, modified string, err error) {
	for try := 1; ; try++ {
		text, modified, err = fetchOnce(client, url)
		if err == nil || errors.Is(err, errNotServed) || try == fetchTries {
			return text, modified, err
		}
		time.Sleep(time.Duration(try) * fetchPause)
	}
}

func fetchOnce(client *http.Client, url string) (text []byte, modified string, err error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		text, err = io.ReadAll(resp.Body)
		return text, resp.Header.Get("Last-Modified"), err
	case http.StatusNotFound:
		return nil, "", errNotServed
	}
	return nil, "", fmt.Errorf("GET %s: %s", url, resp.Status)
}

// A release tag of the OS publishes the coverage report beside the
// release, and the report needs the profile of the tagged commit. A
// tag runs only the checks that its changes reach, so the check job
// that writes the profile often does not run. The publish job then
// takes the copy that the site serves, when that copy describes the
// files at the tag. The run of the tests that writes a profile takes
// minutes, and the copy takes one request.
//
// The argument above gives the commit that the served copy describes:
// the commit of the deploy that serves it. The site serves that commit
// in release.txt. So the copy describes the tag when no file that the
// job reads changed between that commit and the tag, which is the test
// that the plan applies to select the job. The deploy can be newer
// than the tag or older, and the test holds in both directions,
// because a file that differs between the two commits is a change.
//
// A CDN serves the site, and for up to ten minutes it can serve the
// release.txt of one deploy and the profile of another. GitHub Pages
// gave every file of a deploy the same Last-Modified time in the
// deploys checked, so the publish takes the copy only when the two
// times are the same. If Pages stops doing that, the times differ, and
// the publish runs the tests.
//
// When any of these checks fails, the publish writes the profile with
// its own run of the tests. So the copy saves time, and a failure to
// read it costs only that time.

// reuseProfiles writes into the component's directory each profile
// that the site serves, when every one of them describes the files at
// head. It returns an error that gives the reason when it writes none.
func reuseProfiles(root, site, head string, client *http.Client, components map[string]*Component, c *Component) error {
	commit, deployed, err := fetchServed(client, site+"/release.txt")
	if err != nil {
		return fmt.Errorf("the site's release.txt: %w", err)
	}
	served := strings.TrimSpace(string(commit))
	git := Git{Dir: root}
	if !git.HasCommit(served) {
		return fmt.Errorf("the site serves commit %q, which the repository does not hold", served)
	}
	diff, err := git.ReadDiff(components, served, head)
	if err != nil {
		return err
	}
	if file, ok := diff.Workflows[c.Name()]; ok {
		return fmt.Errorf("%s changed since %s", file, served)
	}
	sel := selector{root: root, components: components, producer: ImageProducers(components), diff: diff}
	texts := map[string][]byte{}
	for _, job := range c.Jobs {
		if len(job.Coverage) == 0 {
			continue
		}
		deps := sel.jobDeps(c, job)
		for _, file := range diff.Files {
			if sel.jobReads(c, job, deps, file) {
				return fmt.Errorf("the %s job reads %s, which changed since %s", job.Name, file, served)
			}
		}
		for _, file := range job.Coverage {
			text, modified, err := fetchServed(client, site+"/coverage/"+c.Name()+"/"+file)
			if err != nil {
				return fmt.Errorf("the site's %s: %w", file, err)
			}
			if deployed == "" || modified != deployed {
				return fmt.Errorf("the site's %s is from another deploy than its release.txt", file)
			}
			texts[file] = text
		}
	}
	if len(texts) == 0 {
		return fmt.Errorf("%s has no coverage job", c.Name())
	}
	for file, text := range texts {
		if err := os.WriteFile(filepath.Join(root, c.Dir, file), text, 0o644); err != nil {
			return err
		}
	}
	return nil
}
