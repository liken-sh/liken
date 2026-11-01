package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dependabotConfig is the part of dependabot.yml that the tests read.
type dependabotConfig struct {
	Version int `yaml:"version"`
	Updates []struct {
		Ecosystem string `yaml:"package-ecosystem"`
		Directory string `yaml:"directory"`
		Limit     *int   `yaml:"open-pull-requests-limit"`
		Schedule  struct {
			Interval string `yaml:"interval"`
		} `yaml:"schedule"`
		Groups map[string]struct {
			AppliesTo string   `yaml:"applies-to"`
			Patterns  []string `yaml:"patterns"`
		} `yaml:"groups"`
		Ignore []struct {
			Name string `yaml:"dependency-name"`
		} `yaml:"ignore"`
	} `yaml:"updates"`
}

// dependabotTree is a repository with two Go modules, a manual's
// module, a Cargo workspace with a member crate, a standalone crate,
// and the trees that the configuration leaves out: test data, a cache,
// and a checkout that .gitignore leaves out.
func dependabotTree(t *testing.T) string {
	t.Helper()
	root := writeTree(t, map[string]string{
		"go.mod":                     "module root\n",
		"a/go.mod":                   "module a\n",
		"a/docs/go.mod":              "module a/docs\n",
		"b/Cargo.toml":               "[workspace]\nmembers = [\"c\"]\n",
		"b/Cargo.lock":               "version = 4\n",
		"b/c/Cargo.toml":             "[package]\nname = \"c\"\n",
		"d/Cargo.toml":               "[package]\nname = \"d\"\n",
		"d/Cargo.lock":               "version = 4\n",
		"a/testdata/go.mod":          "module t\n",
		"a/.cache/go.mod":            "module c\n",
		"a/.gitignore":               "/download/\n",
		"a/download/go.mod":          "module x\n",
		"a/download/Cargo.lock":      "version = 4\n",
		"plans/1-history/go.mod":     "module p\n",
		"plans/1-history/Cargo.lock": "version = 4\n",
	})
	if _, err := (Git{Dir: root}).run("init", "-q"); err != nil {
		t.Fatal(err)
	}
	return root
}

func readDependabot(t *testing.T, root string) dependabotConfig {
	t.Helper()
	text, err := Dependabot(root)
	if err != nil {
		t.Fatal(err)
	}
	var config dependabotConfig
	if err := yaml.Unmarshal(text, &config); err != nil {
		t.Fatalf("dependabot.yml does not parse: %v\n%s", err, text)
	}
	return config
}

func TestDependabotListsEveryModuleAndEveryLockfile(t *testing.T) {
	config := readDependabot(t, dependabotTree(t))
	var entries []string
	for _, u := range config.Updates {
		entries = append(entries, u.Ecosystem+" "+u.Directory)
	}
	want := []string{"gomod /", "gomod /a", "gomod /a/docs", "cargo /b", "cargo /d"}
	if config.Version != 2 || !reflect.DeepEqual(entries, want) {
		t.Errorf("version %d, entries %q, want version 2 and %q", config.Version, entries, want)
	}
}

func TestEveryDependabotEntryOpensOnlyGroupedSecurityUpdates(t *testing.T) {
	for _, u := range readDependabot(t, dependabotTree(t)).Updates {
		group, grouped := u.Groups["security"]
		if u.Limit == nil || *u.Limit != 0 || u.Schedule.Interval == "" || len(u.Groups) != 1 || !grouped ||
			group.AppliesTo != "security-updates" || !reflect.DeepEqual(group.Patterns, []string{"*"}) {
			t.Errorf("%s %s opens more than one grouped pull request of security updates: %+v", u.Ecosystem, u.Directory, u)
		}
	}
}

func TestDependabotLeavesTheKubernetesModulesToK3s(t *testing.T) {
	ignored := map[string][]string{}
	for _, u := range readDependabot(t, dependabotTree(t)).Updates {
		for _, i := range u.Ignore {
			ignored[u.Ecosystem+" "+u.Directory] = append(ignored[u.Ecosystem+" "+u.Directory], i.Name)
		}
	}
	k8s := []string{"k8s.io/*", "sigs.k8s.io/*"}
	want := map[string][]string{"gomod /": k8s, "gomod /a": k8s, "gomod /a/docs": k8s}
	if !reflect.DeepEqual(ignored, want) {
		t.Errorf("ignored %q, want %q", ignored, want)
	}
}

func TestDependabotSkipsAModuleThatTheTreeDeleted(t *testing.T) {
	r := newRepo(t, map[string]string{"a/go.mod": "module a\n", "b/go.mod": "module b\n"})
	os.Remove(filepath.Join(r.git.Dir, "b/go.mod"))
	var entries []string
	for _, u := range readDependabot(t, r.git.Dir).Updates {
		entries = append(entries, u.Directory)
	}
	if !reflect.DeepEqual(entries, []string{"/a"}) {
		t.Errorf("entries %q, want only /a", entries)
	}
}

// A new module fails `generate -check` until `make workflows` writes
// its entry, so no component goes without security updates.
func TestGenerateCheckFailsForAModuleThatDependabotDoesNotList(t *testing.T) {
	r := planFixture(t)
	os.MkdirAll(filepath.Join(r.git.Dir, ".github/workflows"), 0o755)
	os.MkdirAll(filepath.Join(r.git.Dir, "liken/flux"), 0o755)
	os.WriteFile(filepath.Join(r.git.Dir, fluxVersionFile), []byte("v2.9.7\n"), 0o644)
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"generate", "-root", r.git.Dir}); err != nil {
		t.Fatal(err)
	}
	r.write("operator/go.mod", "module example.com/operator\n")
	err := w.run([]string{"generate", "-root", r.git.Dir, "-check"})
	if err == nil || !strings.Contains(err.Error(), dependabotFile) {
		t.Errorf("the check gave %v, want a failure that names %s", err, dependabotFile)
	}
}
