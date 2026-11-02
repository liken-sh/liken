package verify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPackagesReadThePublishedSBOMs(t *testing.T) {
	var statement struct {
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(image(t, "sbom.intoto.json"), &statement); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		read  func([]byte) ([]Package, error)
		data  []byte
		count int
		first Package
	}{
		{"the release's CycloneDX SBOM", CycloneDXPackages, release(t, "sbom.cdx.json"), 744,
			Package{Name: "flux", Version: "v2.10.0", PURL: "pkg:github/fluxcd/flux2@v2.10.0"}},
		{"the image's SPDX SBOM", SPDXPackages, statement.Predicate, 21,
			Package{Name: "bluez-static", Version: "5.87"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packages, err := c.read(c.data)
			if err != nil {
				t.Fatal(err)
			}
			if len(packages) != c.count || !reflect.DeepEqual(packages[0], c.first) {
				t.Errorf("read %d packages, the first %+v; want %d, the first %+v", len(packages), packages[0], c.count, c.first)
			}
		})
	}
}

func TestPackagesRefuseADocumentThatIsNotJSON(t *testing.T) {
	cases := []struct {
		name   string
		read   func([]byte) ([]Package, error)
		reason string
	}{
		{"CycloneDX", CycloneDXPackages, "the CycloneDX document does not parse"},
		{"SPDX", SPDXPackages, "the SPDX document does not parse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.read([]byte("{"))
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the read returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}

func TestSummaryWithNoSources(t *testing.T) {
	if got := (Summary{}).String(); got != "0 components" {
		t.Errorf("the summary is %q", got)
	}
}
