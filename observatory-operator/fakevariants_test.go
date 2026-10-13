package main

// Drivers that no simulator is, made from the transcripts of the
// simulators that are. A Flip-Flat is a dust cap and a flat panel in
// one driver, and no simulator serves both interfaces, so its fake
// joins the dust cover's transcripts and the light panel's under one
// device. An SBIG camera claims INDI's filter interface with no wheel
// attached, so its fake is the guide camera with the FILTER bit set in
// DRIVER_INTERFACE. A mount with a GPS joins the telescope simulator's
// transcripts and the GPS simulator's. A test names a variant as a
// driver, with an image, because the indi images hold no such driver.

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
)

// fakeVariant is one driver made from the transcripts of simulators.
type fakeVariant struct {
	// simulators are the directories of indi/inditest/testdata/ whose
	// definitions the driver joins, the first one's first.
	simulators []string
	// device is the name the driver gives its device.
	device string
	// bits is the driver's DRIVER_INTERFACE.
	bits string
}

var fakeVariants = map[string]fakeVariant{
	// DUSTCAP is 1<<9 and LIGHTBOX 1<<10, with AUX, 1<<15
	// (libs/indidevice/basedevice.h).
	"indi_fake_flipflat": {simulators: []string{"lightpanel", "dustcover"}, device: "Flip-Flat Simulator", bits: "34304"},
	// CCD, GUIDER, and FILTER.
	"indi_fake_sbig": {simulators: []string{"guide"}, device: "SBIG Simulator", bits: "22"},
	// TELESCOPE and GPS: a mount with a GPS receiver of its own. The
	// device keeps the telescope simulator's name, so PHD2 and the
	// mount's locks find it as they find that simulator.
	"indi_fake_gpsmount": {simulators: []string{"telescope", "gps"}, device: "Telescope Simulator", bits: "65"},
}

// variantImage is an image for a variant's driver, which a resource names
// because drivers.Resolve knows no variant.
const variantImage = "ghcr.io/example/indi-fake:1.0"

// transcripts joins the definitions of the variant's simulators under
// its device's name, with the variant's own DRIVER_INFO.
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
