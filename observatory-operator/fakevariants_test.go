package main

// Fake drivers for hardware that has no simulator, built by combining
// the transcripts of simulators. A Flip-Flat is a dust cap and a flat
// panel in one driver, and no simulator serves both interfaces, so its
// fake combines the dust cover's transcripts and the light panel's
// under one device. An SBIG camera reports INDI's filter interface
// with no wheel attached, so its fake is the guide camera simulator
// with the FILTER bit set in DRIVER_INTERFACE. A mount with a GPS
// combines the telescope simulator's transcripts and the GPS
// simulator's.

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// fakeVariant is one driver made from the transcripts of simulators.
type fakeVariant struct {
	// simulators are the directories of indi/inditest/testdata/ whose
	// definitions the driver combines. When two simulators define the
	// same property, the earlier one's definition is used.
	simulators []string
	// device is the INDI device name that the driver reports.
	device string
	// bits is the value of the driver's DRIVER_INTERFACE property.
	bits string
}

var fakeVariants = map[string]fakeVariant{
	// DUSTCAP (1<<9), LIGHTBOX (1<<10), and AUX (1<<15), from
	// libs/indidevice/basedevice.h.
	"indi_fake_flipflat": {simulators: []string{"lightpanel", "dustcover"}, device: "Flip-Flat Simulator", bits: "34304"},
	// CCD, GUIDER, and FILTER.
	"indi_fake_sbig": {simulators: []string{"guide"}, device: "SBIG Simulator", bits: "22"},
	// TELESCOPE and GPS: a mount with its own GPS receiver. The
	// device keeps the telescope simulator's name, so PHD2 and the
	// mount's locks find it as they find that simulator.
	"indi_fake_gpsmount": {simulators: []string{"telescope", "gps"}, device: "Telescope Simulator", bits: "65"},
}

// variantImage is the image that a test gives a resource whose driver
// is a variant, because drivers.Resolve has no entry for a variant.
const variantImage = "ghcr.io/example/indi-fake:1.0"

// transcripts combines the definitions of the variant's simulators
// under the variant's device name, with the variant's own DRIVER_INFO.
func (v fakeVariant) transcripts(t *testing.T, exec string) transcriptPair {
	var out transcriptPair
	join := func(into []*inditest.Property, from []*inditest.Property) []*inditest.Property {
		for _, p := range from {
			if slices.ContainsFunc(into, func(o *inditest.Property) bool { return o.Name == p.Name }) {
				continue
			}
			p = p.Clone()
			p.Device = v.device
			if p.Name == "DRIVER_INFO" {
				for i := range p.Members {
					switch p.Members[i].Name {
					case "DRIVER_EXEC":
						p.Members[i].Value = exec
					case "DRIVER_NAME":
						p.Members[i].Value = v.device
					case "DRIVER_INTERFACE":
						p.Members[i].Value = v.bits
					}
				}
			}
			into = append(into, p)
		}
		return into
	}
	for _, simulator := range v.simulators {
		out.baseline = join(out.baseline, definitions(t, simulator, "baseline"))
		out.connect = join(out.connect, definitions(t, simulator, "connect"))
	}
	return out
}
