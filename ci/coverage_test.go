package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reportTree is a repository of three components with coverage: os,
// whose manual is the root of the site; app, whose manual is at
// /app/ and whose report needs a Go and a Rust profile; and lib, which
// has no manual.
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

// servedSite serves the files of a published site, and nothing else.
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
// files, and returns its world and the directory it published into.
func runReports(t *testing.T, root string, served map[string]string) (*world, string) {
	t.Helper()
	w := newWorld(t, &fakeRegistry{})
	dest := t.TempDir()
	if err := w.run([]string{"reports", "-root", root, "-site", servedSite(t, served), "-dest", dest}); err != nil {
		t.Fatal(err)
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

// Each line of the output names the directory whose Makefile renders a
// report, and the report's path in the site. A report renders only
// when every one of its profiles is at hand: written by this run's
// job, or served by the site from the run where that job last ran.
func TestReportsListEachReportWhoseProfilesAreAllAtHand(t *testing.T) {
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
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", file, got, want)
		}
	}
}

// The site publishes each profile it has, beside the reports, so the
// next deploy can take the profile of a job that does not run then.
// A report that cannot render still publishes the profiles it has, so
// its jobs can fill it one at a time.
func TestReportsPublishEveryProfileAtHand(t *testing.T) {
	root := reportTree(t, map[string]string{"app/coverage.out": "mode: set\nthis run\n"})
	_, dest := runReports(t, root, map[string]string{"coverage/lib/coverage.out": "mode: set\nserved\n"})
	cases := map[string]string{
		"coverage/app/coverage.out": "mode: set\nthis run\n",
		"coverage/lib/coverage.out": "mode: set\nserved\n",
	}
	for file, want := range cases {
		got, err := os.ReadFile(filepath.Join(dest, file))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", file, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "coverage/app/coverage-app.xml")); err == nil {
		t.Error("the site publishes a profile that no job wrote")
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

// Only a 404 means that the site serves no profile yet. Any other
// failure stops the deploy: a deploy that went on would publish the
// site without the profile, and the next deploy could not find it.
func TestReportsFailWhenTheSiteFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	w := newWorld(t, &fakeRegistry{})
	err := w.run([]string{"reports", "-root", reportTree(t, nil), "-site", server.URL, "-dest", t.TempDir()})
	if err == nil {
		t.Fatal("reports passed while the site failed")
	}
}

// taggedRepo is an OS whose go job writes a coverage profile. The site
// deployed its first commit, and the tag is a second commit that
// changes the file named, or the same commit when change is "".
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

// A servedFile is one answer of the site: its status, its text, and
// its Last-Modified time, which is the time of its deploy.
type servedFile struct {
	status         int
	text, modified string
}

const deployTime = "Wed, 30 Sep 2026 16:45:41 GMT"

// deployedSite serves release.txt from a deploy of the commit, and the
// OS's profile as given.
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

// The OS's publish takes the profile that the site serves only when
// that profile describes the files at the tag. In every other case the
// command fails, writes nothing, and the publish runs the tests.
func TestThePublishTakesTheServedProfileOnlyWhenItDescribesTheTag(t *testing.T) {
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
			w := newWorld(t, &fakeRegistry{})
			err := w.run([]string{"profile", "-root", root, "-component", "liken", "-site", deployedSite(t, deployed, c.profile)})
			if (err == nil) != (c.want != "") {
				t.Errorf("profile returned %v", err)
			}
			got, _ := os.ReadFile(filepath.Join(root, "liken/coverage.out"))
			if string(got) != c.want {
				t.Errorf("liken/coverage.out holds %q, want %q", got, c.want)
			}
		})
	}
}
