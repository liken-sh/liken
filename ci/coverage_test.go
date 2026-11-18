package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test's server responds immediately, so a fetch that retries does
// not need to wait between requests.
func init() {
	fetchPause = 0
}

// reportTree is a repository of three components with coverage: os,
// whose manual is the root of the site; app, whose manual is at /app/
// and whose report needs a Go profile and a Rust profile; and lib,
// which has no manual.
func reportTree(t *testing.T, profiles map[string]string) string {
	t.Helper()
	files := map[string]string{
		"os/package.toml": "[package]\nname = \"os\"\n[docs]\nprefix = \"\"\n" +
			"[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\ncoverage = [\"coverage.out\"]\n",
		"app/package.toml": "[package]\nname = \"app\"\n[docs]\nprefix = \"app\"\n" +
			"[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\ncoverage = [\"coverage.out\"]\n" +
			"[[jobs]]\nname = \"rust\"\ntoolchain = \"rust\"\nrun = \"make test\"\ncoverage = [\"coverage-app.xml\"]\n",
		"lib/package.toml": "[package]\nname = \"lib\"\n" +
			"[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\ncoverage = [\"coverage.out\"]\n",
		"untested/package.toml": "[package]\nname = \"untested\"\n",
	}
	for name, text := range profiles {
		files[name] = text
	}
	return writeTree(t, files)
}

// servedSite serves the files of a published site and returns 404 for
// every other path.
func servedSite(t *testing.T, files map[string]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(text))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// runReports runs the reports command on the tree against the served
// files, and returns its fake environment and the directory that it published
// into.
func runReports(t *testing.T, root string, served map[string]string) (*fakeEnv, string) {
	t.Helper()
	w := newFakeEnv(t, &fakeRegistry{})
	dest := t.TempDir()
	if err := w.run([]string{"reports", "-root", root, "-site", servedSite(t, served), "-dest", dest}); err != nil {
		t.Fatalf("running reports: %v", err)
	}
	return w, dest
}

var everyProfile = map[string]string{
	"os/coverage.out":      "mode: set\n",
	"app/coverage.out":     "mode: set\n",
	"app/coverage-app.xml": "<coverage/>\n",
	"lib/coverage.out":     "mode: set\n",
}

var everyServedProfile = map[string]string{
	"coverage/os/coverage.out":       "mode: set\n",
	"coverage/app/coverage.out":      "mode: set\n",
	"coverage/app/coverage-app.xml":  "<coverage/>\n",
	"coverage/lib/coverage.out":      "mode: set\n",
	"coverage/untested/coverage.out": "mode: set\n",
}

// Each line of the output has the directory whose Makefile renders a
// report and the report's path on the site. A report renders only when
// all of its profiles are available, either written by this run's job
// or served by the site from the last run of that job.
func TestReportsListEachReportWhoseProfilesAreAllAvailable(t *testing.T) {
	cases := []struct {
		name   string
		disk   map[string]string
		served map[string]string
		want   string
	}{
		{"every job ran", everyProfile, nil,
			"app:app/coverage.html\nlib:coverage/lib.html\nos:coverage.html\n"},
		{"no job ran", nil, everyServedProfile,
			"app:app/coverage.html\nlib:coverage/lib.html\nos:coverage.html\n"},
		{"only app's go job ran", map[string]string{"app/coverage.out": "mode: set\n"}, everyServedProfile,
			"app:app/coverage.html\nlib:coverage/lib.html\nos:coverage.html\n"},
		{"app's rust job has no profile anywhere", map[string]string{"app/coverage.out": "mode: set\n"}, nil,
			""},
		{"lib's profile is served, app's rust profile is not", nil,
			map[string]string{"coverage/app/coverage.out": "mode: set\n", "coverage/lib/coverage.out": "mode: set\n"},
			"lib:coverage/lib.html\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, _ := runReports(t, reportTree(t, c.disk), c.served)
			if w.out.String() != c.want {
				t.Errorf("reports printed %q, want %q", w.out.String(), c.want)
			}
		})
	}
}

func TestReportsTakeTheServedProfileOfAJobThatDidNotRun(t *testing.T) {
	root := reportTree(t, map[string]string{"app/coverage.out": "mode: set\nthis run\n"})
	runReports(t, root, map[string]string{
		"coverage/app/coverage.out":     "mode: set\nan earlier run\n",
		"coverage/app/coverage-app.xml": "<coverage>an earlier run</coverage>\n",
	})
	cases := map[string]string{
		"app/coverage.out":     "mode: set\nthis run\n",
		"app/coverage-app.xml": "<coverage>an earlier run</coverage>\n",
	}
	for file, want := range cases {
		got, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", file, got, want)
		}
	}
}

// The site publishes every available profile beside the reports, so
// the next deploy can reuse the profile of a job that does not run
// then. A report that cannot render still publishes the profiles that
// are available, so its jobs can supply the missing ones one at a
// time.
func TestReportsPublishEveryAvailableProfile(t *testing.T) {
	root := reportTree(t, map[string]string{"app/coverage.out": "mode: set\nthis run\n"})
	_, dest := runReports(t, root, map[string]string{"coverage/lib/coverage.out": "mode: set\nserved\n"})
	cases := map[string]string{
		"coverage/app/coverage.out": "mode: set\nthis run\n",
		"coverage/lib/coverage.out": "mode: set\nserved\n",
	}
	for file, want := range cases {
		got, err := os.ReadFile(filepath.Join(dest, file))
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", file, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "coverage/app/coverage-app.xml")); err == nil {
		t.Error("the site publishes coverage-app.xml, which no job wrote, want it absent")
	}
}

func TestReportsNameTheJobWhoseProfileIsMissing(t *testing.T) {
	w, _ := runReports(t, reportTree(t, map[string]string{"app/coverage.out": "mode: set\n"}),
		map[string]string{"coverage/lib/coverage.out": "mode: set\n", "coverage/os/coverage.out": "mode: set\n"})
	want := "app: the site leaves out the coverage report: the rust job did not run, and the site serves no coverage-app.xml from an earlier run\n"
	if w.err.String() != want {
		t.Errorf("reports warned %q, want %q", w.err.String(), want)
	}
}

// Only a 404 means that the site has no copy of the profile. Any other
// failure stops the deploy.
func TestReportsFailWhenTheSiteFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	w := newFakeEnv(t, &fakeRegistry{})
	err := w.run([]string{"reports", "-root", reportTree(t, nil), "-site", server.URL, "-dest", t.TempDir()})
	if err == nil {
		t.Fatal("reports returned no error while the site returned 503, want an error")
	}
}

// taggedRepo is a repository of the `liken` OS whose go job writes a
// coverage profile. The site deployed the first commit. The tag is a
// second commit that changes the file named by change, or the first
// commit when change is empty.
func taggedRepo(t *testing.T, change string) (root, deployed string) {
	t.Helper()
	r := newRepo(t, map[string]string{
		"liken/package.toml":  "[package]\nname = \"liken\"\n[docs]\nprefix = \"\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test-go\"\ncoverage = [\"coverage.out\"]\n[outputs]\nchannel = true\n",
		"liken/init/main.go":  "package main\n",
		"liken/docs/index.md": "a manual\n",
	})
	deployed = r.run("rev-parse", "HEAD")
	if change != "" {
		r.write(change, "changed\n")
		r.commit("the tag")
	}
	return r.git.Dir, deployed
}

// A servedFile is one response of the site: its status, its body, and
// its Last-Modified time, which is the time of its deploy.
type servedFile struct {
	status         int
	text, modified string
}

const deployTime = "Wed, 30 Sep 2026 16:45:41 GMT"

// deployedSite serves a release.txt for a deploy of the commit, and the
// `liken` OS's profile as the argument describes it.
func deployedSite(t *testing.T, commit string, profile servedFile) string {
	t.Helper()
	files := map[string]servedFile{
		"/release.txt":                 {http.StatusOK, commit + "\n", deployTime},
		"/coverage/liken/coverage.out": profile,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if f.modified != "" {
			w.Header().Set("Last-Modified", f.modified)
		}
		w.WriteHeader(f.status)
		w.Write([]byte(f.text))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// The publish of the `liken` OS reuses the served profile only when it
// describes the files at the tag. In every other case the command
// fails and writes nothing, and the publish runs the tests.
func TestThePublishReusesTheServedProfileOnlyWhenItMatchesTheTag(t *testing.T) {
	const profileText = "mode: atomic\nserved\n"
	cases := []struct {
		name    string
		change  string
		profile servedFile
		want    string
	}{
		{"served, and the tag is the deployed commit", "", servedFile{http.StatusOK, profileText, deployTime}, profileText},
		{"served, and a file outside the OS changed", "README.md", servedFile{http.StatusOK, profileText, deployTime}, profileText},
		{"served, and only the manual changed", "liken/docs/index.md", servedFile{http.StatusOK, profileText, deployTime}, profileText},
		{"a file the go job reads changed since the deploy", "liken/init/main.go", servedFile{http.StatusOK, profileText, deployTime}, ""},
		{"the site serves no profile", "", servedFile{http.StatusNotFound, "", ""}, ""},
		{"the site fails", "", servedFile{http.StatusServiceUnavailable, "", ""}, ""},
		{"the CDN serves the profile of another deploy", "", servedFile{http.StatusOK, profileText, "Tue, 29 Sep 2026 12:00:00 GMT"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, deployed := taggedRepo(t, c.change)
			w := newFakeEnv(t, &fakeRegistry{})
			err := w.run([]string{"profile", "-root", root, "-component", "liken", "-site", deployedSite(t, deployed, c.profile)})
			if (err == nil) != (c.want != "") {
				t.Errorf("profile returned %v, want an error exactly when the profile is not reusable", err)
			}
			got, _ := os.ReadFile(filepath.Join(root, "liken/coverage.out"))
			if string(got) != c.want {
				t.Errorf("liken/coverage.out holds %q, want %q", got, c.want)
			}
		})
	}
}

func TestAFetchRetriesAfterAServerError(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		want     string
		tries    int
	}{
		{name: "one 503, then the file", statuses: []int{503, 200}, want: "profile", tries: 2},
		{name: "a 404", statuses: []int{404}, want: "", tries: 1},
		{name: "503 on every try", statuses: []int{503, 503, 503}, want: "", tries: fetchTries},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status := test.statuses[min(tries, len(test.statuses)-1)]
				tries++
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte("profile"))
				}
			}))
			defer server.Close()

			text, _, _ := fetchServed(server.Client(), server.URL+"/coverage.out")

			if string(text) != test.want || tries != test.tries {
				t.Errorf("fetch = %q after %d tries, want %q after %d", text, tries, test.want, test.tries)
			}
		})
	}
}
