package drivers

import (
	"errors"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The drivers of plan 06's table, with the images the indi build puts
// them in. The simulators run from indi-simulators, which holds the
// star catalog that the camera simulators draw from.
func TestEveryDriverResolvesToTheImageThatHoldsIt(t *testing.T) {
	cases := []struct {
		driver string
		image  string
	}{
		{"indi_simulator_telescope", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_ccd", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_guide", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_wheel", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_focus", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_rotator", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_dustcover", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_lightpanel", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_pac", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_gps", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_dome", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_weather", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_sqm", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_io", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		{"indi_simulator_receiver", "ghcr.io/liken-sh/indi-simulators:" + Tag},
		// Core drivers come from indi-bin, which the indi image holds.
		{"indi_lx200generic", "ghcr.io/liken-sh/indi:" + Tag},
		{"indi_celestron_gps", "ghcr.io/liken-sh/indi:" + Tag},
		// A third-party driver comes from its family's image.
		{"indi_eqmod_telescope", "ghcr.io/liken-sh/indi-open:" + Tag},
		{"indi_asi_ccd", "ghcr.io/liken-sh/indi-zwo:" + Tag},
		{"indi_asi_wheel", "ghcr.io/liken-sh/indi-zwo:" + Tag},
	}
	for _, c := range cases {
		t.Run(c.driver, func(t *testing.T) {
			image, err := Resolve(observatory.Driver{Name: c.driver})
			if err != nil || image != c.image {
				t.Errorf("Resolve(%s) = %q, %v; want %q", c.driver, image, err, c.image)
			}
		})
	}
}

func TestAnImageInTheSpecIsUsedAsWritten(t *testing.T) {
	image, err := Resolve(observatory.Driver{Name: "acme-ccd", Image: "ghcr.io/example/acme:1.0"})
	if err != nil || image != "ghcr.io/example/acme:1.0" {
		t.Errorf("Resolve = %q, %v; want the image as written", image, err)
	}
}

// A third-party driver that the indi build puts in no image cannot
// run, and the error says so.
func TestAnUnpublishedDriverHasNoImage(t *testing.T) {
	_, err := Resolve(observatory.Driver{Name: "indi_fishcamp_ccd"})
	if !errors.Is(err, ErrUnpublished) {
		t.Errorf("Resolve(indi_fishcamp_ccd) = %v, want ErrUnpublished", err)
	}
}

func TestTheServerRunsTheIndiImage(t *testing.T) {
	if got := ServerImage(); got != "ghcr.io/liken-sh/indi:"+Tag {
		t.Errorf("ServerImage() = %q", got)
	}
}
