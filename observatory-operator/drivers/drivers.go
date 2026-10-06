// Package drivers resolves an INDI driver's name to the image that
// holds the driver. A device resource names its driver, such as
// indi_asi_ccd, and the operator runs the device's pod from the image
// that the indi build puts that driver in.
//
// The lists in indi/images/ at the top of the repository decide each
// image's drivers, and the indi build refuses a third-party driver that
// is in no list or in two. generated.go holds those lists as a map, at
// the tag of indi/package.toml, so each operator release runs the
// images it was tested with. The core drivers and the simulators come
// from libindi's own package, which the lists name as a whole, so a
// rule below resolves them.
package drivers

//go:generate go test -run TestTheMapIsCurrent -update

import (
	"errors"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// Registry is where the indi build publishes its images.
const Registry = "ghcr.io/liken-sh"

// ErrUnpublished is the error of a driver that the indi build installs
// and puts in no image, such as a camera that its vendor no longer
// sells. indi/images/unpublished lists them.
var ErrUnpublished = errors.New("no image of the indi build holds this driver")

// simulatorPrefix starts the name of every simulator in libindi. The
// simulators run from indi-simulators, which adds the star catalog that
// the camera simulators draw their frames from. A simulator also runs
// from indi, but the catalog would be missing.
const simulatorPrefix = "indi_simulator_"

// Resolve answers the image of a device's driver: the image the spec
// names, as written, or the image of the indi build that holds the
// driver.
func Resolve(driver observatory.Driver) (string, error) {
	if driver.Image != "" {
		return driver.Image, nil
	}
	name := driver.Name
	switch family, listed := families[name]; {
	case listed && family == "":
		return "", fmt.Errorf("%s: %w; name an image in spec.driver.image", name, ErrUnpublished)
	case listed:
		return image("indi-" + family), nil
	case strings.HasPrefix(name, simulatorPrefix):
		return image("indi-simulators"), nil
	}
	// Every other driver is a core driver from libindi's indi-bin
	// package, which the indi image holds whole.
	return image("indi"), nil
}

// ServerImage answers the image that runs indiserver and the shim.
func ServerImage() string { return image("indi") }

// GuiderImage answers the image that runs PHD2. The indi build makes
// it on indi, so PHD2 links the libindi of the server it talks to, and
// it carries indi's tag.
func GuiderImage() string { return image("indi-phd2") }

// CompositorImage answers the image of the guider's compositor: the
// weston image at the tag that weston/package.toml pins, which
// generated.go copies at build time. display-operator's image builds
// FROM weston at the same commit, so a node that runs display-operator
// already holds these layers, and the guider pulls no new image. Never
// build weston into another image or name another tag here: one bump
// of weston moves the guider and display-operator together.
func CompositorImage() string { return Registry + "/weston:" + WestonTag }

func image(name string) string { return Registry + "/" + name + ":" + Tag }
