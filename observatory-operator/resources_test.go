package main

// A reference in a procedure names resources by kind and name, from the
// resource that holds it.

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestAReferenceNamesResourcesFromWhereItIs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		tr := w.operator().snapshot()
		from := func(kind observatory.Kind, name string) resource {
			r, ok := tr.resource(kind, name)
			if !ok {
				t.Fatalf("no %s %s", kind.Name, name)
			}
			return r
		}
		dome := from(observatory.DomeKind, "lab")
		cases := []struct {
			name      string
			from      resource
			kind, ref string
			many      bool
			want      string
		}{
			{"no kind names the resource itself", dome, "", "", false, "Dome lab"},
			{"a device by name", dome, "WeatherStation", "lab", false, "WeatherStation lab"},
			{"a tube by name", dome, "OpticalTube", "east-refractor", false, "OpticalTube east-refractor"},
			{"a train by name", dome, "OpticalTrain", "west-imaging", false, "OpticalTrain west-imaging"},
			{"a guider by name", dome, "Guider", "east", false, "Guider east"},
			{"a tube that does not exist", dome, "OpticalTube", "north", false, "f: no OpticalTube north"},
			{"a train that does not exist", dome, "OpticalTrain", "north", false, "f: no OpticalTrain north"},
			{"a guider that does not exist", dome, "Guider", "north", false, "f: no Guider north"},
			{"a device that does not exist", dome, "WeatherStation", "roof", false, "f: no WeatherStation roof"},
			{"a kind that does not exist", dome, "Planet", "mars", false, "f: no kind Planet"},
			{"a Reservation", dome, "Reservation", "east-tonight", false, "f: no kind Reservation"},
			{"the observatory of a device", dome, "Observatory", "", false, "Observatory lab"},
			{"the observatory of itself", from(observatory.ObservatoryKind, "lab"), "Observatory", "", false, "Observatory lab"},
			{"the telescope of a train's device", from(observatory.CameraKind, "east-main"), "Telescope", "", false, "Telescope east"},
			{"the telescope of a telescope", from(observatory.TelescopeKind, "west"), "Telescope", "", false, "Telescope west"},
			{"the telescope of an observatory's device", dome, "Telescope", "", false, "f: Dome lab belongs to no Telescope"},
			{"the observatory of a device on the shelf", from(observatory.FocuserKind, "spare"), "Observatory", "", false,
				"f: Focuser spare belongs to no Observatory"},
			{"a device kind with no name, where one is needed", dome, "Mount", "", false, "f: a Mount reference needs a name"},
			{"every mount in the observatory", dome, "Mount", "", true, "Mount east, Mount west"},
			{"every camera in the observatory", dome, "Camera", "", true, "Camera east-guide, Camera east-main, Camera west-main"},
			{"every telescope", dome, "Telescope", "", true, "f: Dome lab belongs to no Telescope"},
			{"every train", dome, "OpticalTrain", "", true, "OpticalTrain east-guiding, OpticalTrain east-imaging, OpticalTrain west-imaging"},
			{"every tube", dome, "OpticalTube", "", true, "OpticalTube east-guidescope, OpticalTube east-refractor, OpticalTube west-newtonian"},
			{"every guider", dome, "Guider", "", true, "Guider east"},
			{"every observatory device of a kind", from(observatory.MountKind, "west"), "WeatherStation", "", true, "WeatherStation lab"},
		}
		for _, c := range cases {
			targets, err := tr.resolve(c.from, "f", c.kind, c.ref, c.many)
			got := fmt.Sprint(err)
			if err == nil {
				var names []string
				for _, g := range targets {
					names = append(names, g.String())
				}
				got = strings.Join(names, ", ")
			}
			if got != c.want {
				t.Errorf("%s: resolve = %q, want %q", c.name, got, c.want)
			}
		}
	})
}
