package linkcheck

import "testing"

// These cases mirror ids taken from a site built with the pinned Hugo
// binary, so the test asserts Hugo's behavior, not this package's
// opinion of it. The dotted paths are the headings crdref generates;
// the sentence cases are headings from the hand-written guides.
func TestAnchor(t *testing.T) {
	for text, want := range map[string]string{
		"spec.releases.catalog":               "specreleasescatalog",
		"spec.network.interfaces[]":           "specnetworkinterfaces",
		"spec.storage.biosBoot":               "specstoragebiosboot",
		"spec.features.*":                     "specfeatures",
		"5. Boot each machine from the stick": "5-boot-each-machine-from-the-stick",
		"First, run the hardware report":      "first-run-the-hardware-report",
		"liken adopt":                         "liken-adopt",
		"under_score heading":                 "under_score-heading",
		"dash-already - there":                "dash-already---there",
	} {
		if got := Anchor(text); got != want {
			t.Errorf("Anchor(%q) = %q, want %q", text, got, want)
		}
	}
}
