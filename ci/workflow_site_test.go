package main

import (
	"reflect"
	"strings"
	"testing"
)

// The site takes a coverage profile only from a job that ran, because a
// job that did not run uploaded nothing.
func TestTheSiteTakesCoverageOnlyFromAJobThatRan(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"m\"\ncoverage = [\"coverage.out\"]\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "if: ${{ fromJSON(needs.plan.outputs.components)['app'].check && contains(fromJSON(needs.plan.outputs.components)['app'].jobs, 'go') }}\n        with:\n          name: coverage-app-go"
	if !strings.Contains(string(files[".github/workflows/ci.yaml"]), want) {
		t.Errorf("the site's download lacks %q", want)
	}
}

// The site compiles Hugo in every manual's module, so it restores the
// Hugo build cache that the docs jobs save, saves its own on main, and
// builds the manuals in parallel.
func TestTheSiteSharesTheHugoBuildCacheAndBuildsInParallel(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/ci.yaml"])
	for _, want := range []string{
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('**/docs/go.sum') }}-${{ github.sha }}\n" +
			"          restore-keys: |\n            hugo-build-${{ runner.os }}-${{ hashFiles('**/docs/go.sum') }}-\n            hugo-build-${{ runner.os }}-\n",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.hugo-cache.outputs.cache-hit != 'true' }}\n        uses: actions/cache/save@v6",
		"run: make -j\"$(nproc)\" site SITE_URL=https://liken.sh\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the site job lacks %q", want)
		}
	}
}

// The site reads only the tree and the coverage artifacts of the check
// jobs, so it waits for the checks and not for the publishes. The
// release record lists every published version, so it waits for the
// publishes too.
func TestTheSiteWaitsForTheChecksAndTheRecordForThePublishes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/package.toml":   "[package]\nname = \"lib\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[depends]\ncomponents = [\"lib\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/flux/VERSION": "v2.9.7\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	cases := map[string][]any{
		"site":   {"plan", "lib", "app"},
		"record": {"plan", "lib", "app", "app-publish"},
	}
	for name, needs := range cases {
		if got := jobs[name]["needs"]; !reflect.DeepEqual(got, needs) {
			t.Errorf("%s needs %v, want %v", name, got, needs)
		}
	}
}
