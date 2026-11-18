package main

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shardedJobs decodes the fields of a component's workflow jobs that
// the sharding test checks.
type shardedJobs struct {
	Jobs map[string]struct {
		If       string `yaml:"if"`
		Needs    string `yaml:"needs"`
		Strategy struct {
			FailFast *bool `yaml:"fail-fast"`
			Matrix   struct {
				Shard []int `yaml:"shard"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Env   map[string]string `yaml:"env"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func shardedTree(t *testing.T, job string) string {
	return writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\n" + job,
	})
}

// A sharded job runs shard-run on one runner for each shard, and then
// runs its own command on the shards' coverage files. The plan job
// selects jobs by name, and the site job downloads the artifact
// coverage-<component>-<job>. So the shard job runs when go is
// selected, and go uploads the merged coverage under its own name.
func TestAShardedJobRunsItsShardsAndThenMergesThem(t *testing.T) {
	root := shardedTree(t, "run = \"make gate\"\nshards = 3\nshard-run = \"make shard\"\ncoverage = [\"coverage.out\"]\nenv = { CGO_ENABLED = \"1\" }\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	var workflow shardedJobs
	if err := yaml.Unmarshal(files[".github/workflows/component-app.yaml"], &workflow); err != nil {
		t.Fatalf("reading the generated workflow: %v", err)
	}
	shards, gate := workflow.Jobs["shard-go"], workflow.Jobs["go"]

	selects := "contains(fromJSON(inputs.jobs), 'go')"
	if !strings.Contains(shards.If, selects) || !strings.Contains(gate.If, selects) {
		t.Errorf("the conditions are %q and %q, want both to select the job go", shards.If, gate.If)
	}
	if got := shards.Strategy.Matrix.Shard; !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("the shards are %v, want [1 2 3]", got)
	}
	if shards.Strategy.FailFast == nil || *shards.Strategy.FailFast {
		t.Error("a failed shard cancels the others, want fail-fast to be false")
	}
	if want := map[string]string{"SHARD": "${{ matrix.shard }}", "SHARDS": "3", "CGO_ENABLED": "1"}; !reflect.DeepEqual(shards.Env, want) {
		t.Errorf("the shards' env is %v, want %v", shards.Env, want)
	}
	if gate.Needs != "shard-go" {
		t.Errorf("the job needs %q, want the shards", gate.Needs)
	}
	if _, ok := gate.Env["SHARD"]; ok {
		t.Error("the job sets SHARD, want only the shards to set it")
	}

	var shardRuns, gateRuns, shardUploads, gateUploads, downloads []string
	for _, step := range shards.Steps {
		if step.Run != "" {
			shardRuns = append(shardRuns, step.Run)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			shardUploads = append(shardUploads, step.With["name"])
		}
	}
	for _, step := range gate.Steps {
		if step.Run != "" {
			gateRuns = append(gateRuns, step.Run)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			gateUploads = append(gateUploads, step.With["name"])
		}
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			downloads = append(downloads, step.With["pattern"]+" -> "+step.With["path"])
		}
	}
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"the shards run", shardRuns, []string{"make shard"}},
		{"the job runs", gateRuns, []string{"make gate"}},
		{"the shards upload", shardUploads, []string{"coverage-app-shard-go-${{ matrix.shard }}"}},
		{"the job uploads", gateUploads, []string{"coverage-app-go"}},
		{"the job downloads", downloads, []string{"coverage-app-shard-go-* -> app/coverage-shards"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s %q, want %q", c.what, c.got, c.want)
		}
	}
}

func TestAShardedJobIsDeclaredWhole(t *testing.T) {
	for _, c := range []struct {
		name, job, err string
	}{
		{"shards with no shard-run", "run = \"make gate\"\nshards = 2\ncoverage = [\"c.out\"]\n", "need a shard-run command"},
		{"a shard-run with no shards", "run = \"make gate\"\nshard-run = \"make shard\"\ncoverage = [\"c.out\"]\n", "need a shard-run command"},
		{"shards with no coverage", "run = \"make gate\"\nshards = 2\nshard-run = \"make shard\"\n", "needs coverage files"},
		{"a job named for the shards", "run = \"make gate\"\nshards = 2\nshard-run = \"make shard\"\ncoverage = [\"c.out\"]\n[[jobs]]\nname = \"shard-go\"\ntoolchain = \"go\"\nrun = \"make other\"\n", "has the name that the shards of job"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadComponents(shardedTree(t, c.job))
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("the error is %v, want one that contains %q", err, c.err)
			}
		})
	}
}

// When a Go build cache misses its own key, it restores the newest
// entry whose key begins with go-build-...-<job>-. So the job go would
// restore the -race build of the job go-race.
func TestNoGoJobRestoresTheBuildCacheOfAnother(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n[[jobs]]\nname = \"go-race\"\ntoolchain = \"go\"\nrun = \"make race\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "restores the build cache of job \"go-race\"") {
		t.Errorf("the error is %v, want one about the build caches of the jobs", err)
	}
}
