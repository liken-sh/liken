package main

import (
	"strings"
	"testing"
)

// Code scanning keeps each upload under the ref that the upload names,
// and its view of the default branch reads only the uploads on main. A
// release runs on a tag, so an upload that named the run's own ref
// would stay out of that view until the next scheduled scan on main.
// So every upload of a scan names main when the run's ref is a tag,
// with the run's commit, which a release tag takes from main.
func TestEveryScanUploadOfATagRunNamesMain(t *testing.T) {
	components, err := LoadComponents("..")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows("..", components)
	if err != nil {
		t.Fatal(err)
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
					t.Errorf("%s, job %s: the upload of %v names the ref %v and the sha %v", name, job, with["category"], with["ref"], with["sha"])
				}
			}
		}
	}
	if uploads == 0 {
		t.Error("the generated workflows upload no scan")
	}
}
