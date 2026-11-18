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

// A component's coverage report combines the profiles that its check
// jobs write. A push to main runs only the jobs that read a changed file,
// so a job that did not run leaves no profile on disk. The site
// publishes every profile at coverage/<component>/<file>, and the next
// deploy reads the profile of each job that did not run from there.
//
// The served profile matches this commit. A job that did not run reads
// no file that changed since the newest passing run on main. Every run
// of a coverage job deploys the site, and a run whose deploy fails does
// not pass. So the served profile comes from a run in which the job read
// the same files as this commit.
//
// A report renders only when all of its profiles are available, because
// a report that lacks one would show lower coverage with no
// explanation. The site leaves that report out and prints the name of
// the job that it needs. It still publishes the profiles that it has, so each job
// supplies its own profile the next time it runs.

// report is one component's coverage report: the component, the
// report's path on the site, and the profile file that each job
// writes. The component's directory has the Makefile that renders it.
type report struct {
	component *Component
	path      string
	profiles  []profile
}

// profile is a coverage file that a job writes, relative to the
// component's directory.
type profile struct{ job, file string }

// coverageReports returns the report of every component that has a
// coverage job. A component with a manual publishes its report beside
// the manual, at coverage.html. A component without a manual publishes
// it at coverage/<name>.html.
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

// errNotServed means that the site responded 404 for the file.
var errNotServed = errors.New("not served")

// gatherReports copies every available profile into the component's
// directory, where its coverage-report target reads it, and into dest,
// the tree of the site. It prints the directory and the site path of
// each report that can render. A request that fails with anything but
// a 404 returns an error and stops the deploy, because a deploy without
// the profile would remove it from the site and the next deploy could
// not recover it. The failed deploy fails the run, so the next push to
// main runs the job again.
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
				return fmt.Errorf("gathering %s of %s: %w", p.file, c.Name(), err)
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

// A fetch makes up to fetchTries requests, and waits try * fetchPause
// before each retry. The site's CDN sometimes returns a 5xx, so a fetch
// retries every failure except a 404, which means that the site does
// not serve the file.
const fetchTries = 3

var fetchPause = 2 * time.Second

// fetchServed reads one file from the site and returns its
// Last-Modified header.
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

// A release tag of the `liken` OS publishes a coverage report beside
// the release, from the profile of the tagged commit. A tag runs only
// the checks that read a changed file, so the job that writes the profile
// often does not run. Then the publish job reuses the profile that the
// site serves, when it matches the files at the tag, and saves running
// the tests.
//
// release.txt on the site contains the commit of the site's deploy. The
// served profile matches the tag when no file that the job
// reads differs between that commit and the tag, which is the test the
// plan uses to select the job. The test works whether the deploy is
// older or newer than the tag, because the diff between the two commits
// lists a changed file either way.
//
// A CDN serves the site, and for up to ten minutes it can serve
// release.txt from one deploy and a profile from another. GitHub Pages
// gave every file of one deploy the same Last-Modified time in the
// deploys that were checked, so the publish reuses a profile only when
// its Last-Modified equals that of release.txt. If Pages stops setting
// equal times, this check fails and the publish runs the tests.
//
// When any check fails, the publish runs the tests to write the
// profile.

// reuseProfiles writes each profile that the site serves into the
// component's directory, but only when every profile describes the
// files at head. When it writes nothing, it returns an error that
// gives the reason.
func reuseProfiles(root, site, head string, client *http.Client, components map[string]*Component, c *Component) error {
	commit, deployed, err := fetchServed(client, site+"/release.txt")
	if err != nil {
		return fmt.Errorf("reading the site's release.txt: %w", err)
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
		return fmt.Errorf("%s changed since the site's deploy at %s", file, served)
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
				return fmt.Errorf("the %s job reads %s, which changed since the site's deploy at %s", job.Name, file, served)
			}
		}
		for _, file := range job.Coverage {
			text, modified, err := fetchServed(client, site+"/coverage/"+c.Name()+"/"+file)
			if err != nil {
				return fmt.Errorf("reading the site's %s: %w", file, err)
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
