package main

import (
	"strings"
	"testing"
)

// Code scanning files each upload under the ref that the upload sets,
// and shows the default branch only from uploads for main. A release
// runs on a tag, so an upload under the tag's ref would not show on
// main until the next scheduled scan. So when a run's ref is a tag,
// every SARIF upload sets the ref refs/heads/main and the run's commit, which
// is a commit on main.
func TestEveryScanUploadOfATagRunUsesTheRefOfMain(t *testing.T) {
	components, err := LoadComponents("..")
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows("..", components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	uploads := 0
	for name, data := range files {
		if !strings.HasPrefix(name, ".github/workflows/") {
			continue
		}
		for job, body := range workflowJobs(t, data) {
			steps, _ := body["steps"].([]any)
			for _, step := range steps {
				step := step.(map[string]any)
				uses, _ := step["uses"].(string)
				if !strings.HasPrefix(uses, "github/codeql-action/upload-sarif@") {
					continue
				}
				uploads++
				with := step["with"].(map[string]any)
				if with["ref"] != "${{ startsWith(github.ref, 'refs/tags/') && 'refs/heads/main' || github.ref }}" || with["sha"] != "${{ github.sha }}" {
					t.Errorf("%s, job %s: the scan upload with category %v has ref %v and sha %v, want the ref to be main", name, job, with["category"], with["ref"], with["sha"])
				}
			}
		}
	}
	if uploads == 0 {
		t.Error("the generated workflows upload no scan, want at least one")
	}
}
