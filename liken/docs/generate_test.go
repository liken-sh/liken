// The docs module root holds no program, only this test. The
// generator it exercises is crdref, pinned in go.mod as a tool
// dependency, so this package exists to give the test a home.
package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The real CRDs must generate without error, and their well-known
// sections must appear. The golden test pins the format; this test
// pins the generator to the schemas it exists for.
// The golden test is beside crdref in the brand component. crdref
// is a program, not an importable package, so this test runs the
// pinned tool the way the Makefile rules do.
func TestGenerateHandlesTheRealCRDs(t *testing.T) {
	for _, tc := range []struct {
		path    string
		heading string
	}{
		{"../machine/manifests/machines-crd.yaml", "### spec.storage"},
		{"../cluster/manifests/clusters-crd.yaml", "### spec.releases"},
		{"../disruption/manifests/disruptions-crd.yaml", "### status.conditions[]"},
		{"../disruption/manifests/machinedisruptionbudgets-crd.yaml", "### spec.selector"},
	} {
		out := filepath.Join(t.TempDir(), "page.md")
		cmd := exec.Command("go", "tool", "crdref", tc.path, out)
		if message, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generating a page from %s: %v: %s", tc.path, err, message)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("reading the generated page of %s: %v", tc.path, err)
		}
		if !strings.Contains(string(got), tc.heading) {
			t.Errorf("the page generated from %s has no heading %q", tc.path, tc.heading)
		}
	}
}
