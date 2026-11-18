package main

import "testing"

// brand/package.toml leaves its prose files out of its outputs,
// because no build reads them. So an edit to the prose releases none
// of the components that depend on brand, and an edit to the
// stylesheet releases all of them. The test reads the repository's own
// package.toml files.
func TestBrandsProseReleasesNothingAndItsStylesheetReleasesEveryUser(t *testing.T) {
	components, err := LoadComponents("..")
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	cases := []struct {
		file     string
		releases bool
	}{
		{"brand/voice.md", false},
		{"brand/motion.md", false},
		{"brand/editing-prompt.md", false},
		{"brand/liken.css", true},
	}
	for _, c := range cases {
		for _, user := range []string{"liken", "media-operator", "library-operator"} {
			changed, why := ReleaseChanged(components, user, Diff{Files: []string{c.file}})
			if changed != c.releases {
				t.Errorf("%s releases %s: %v (%s), want %v", c.file, user, changed, why, c.releases)
			}
		}
	}
}
