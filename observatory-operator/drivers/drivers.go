// Package drivers resolves an INDI driver's name to the image that
// contains the driver. A device resource names its driver, such as
// indi_asi_ccd, and the operator runs the device's pod from the image
// that the indi build puts that driver in.
//
// The lists in indi/images/ at the top of the repository decide which
// drivers each image contains. The indi build fails on a third-party
// driver that is in no list or in two. generated.go holds those lists
// as a map, with the tag from indi/package.toml, so each operator
// release runs the images that it was tested with. The core drivers
// and the simulators come from libindi's own package, which the lists
// name as a whole, so Resolve matches them by rule.
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

// ErrUnpublished is the error for a driver that the indi build
// installs but puts in no image. indi/images/unpublished lists these
// drivers.
var ErrUnpublished = errors.New("no image of the indi build includes this driver")

// simulatorPrefix starts the name of every simulator driver in
// libindi. The simulators also run from the indi image, but only
// indi-simulators has the star catalog that the camera simulators draw
// their frames from.
const simulatorPrefix = "indi_simulator_"

// Resolve returns the image that runs a device's driver: spec.image
// as written when it is set, and otherwise the image of the indi build
// that contains the driver.
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
	// package, and the indi image contains all of that package.
	return image("indi"), nil
}

// ServerImage returns the image that runs indiserver and the shim.
func ServerImage() string { return image("indi") }

// GuiderImage returns the image that runs PHD2. The indi build makes
// it from the indi image, so PHD2 links the same libindi as the server
// it talks to, and it has the same tag.
func GuiderImage() string { return image("indi-phd2") }

// CompositorImage returns the image of the guider's compositor: the
// weston image, at the tag that weston/package.toml pins. generated.go
// holds a copy of the tag, and TestTheMapIsCurrent fails when the copy
// is stale. display-operator's image builds FROM weston at the same
// commit, so a node that runs display-operator already has these
// layers and the guider pulls nothing new. Keep this tag and do not
// build weston into another image, so that one bump of weston updates
// the guider and display-operator together.
func CompositorImage() string { return Registry + "/weston:" + WestonTag }

func image(name string) string { return Registry + "/" + name + ":" + Tag }
