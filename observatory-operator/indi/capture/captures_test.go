package capture

// The captures in indi/standard/ record the properties that each INDI
// interface defines, as the bare drivers define them in the libindi
// that indi/package.toml pins. These tests fail when a bump of INDI
// leaves the captures stale, which `make captures` fixes.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	standardDir = filepath.Join("..", "standard")
	indiPins    = filepath.Join("..", "..", "..", "indi", "package.toml")
)

func readCapture(t *testing.T, name string) Capture {
	t.Helper()
	file, err := os.Open(filepath.Join(standardDir, name+".xml"))
	if err != nil {
		t.Fatalf("opening the capture %s: %v", name, err)
	}
	defer file.Close()
	c, err := Read(file)
	if err != nil {
		t.Fatalf("reading the capture %s: %v", name, err)
	}
	return c
}

func captureNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(standardDir, "*.xml"))
	if err != nil {
		t.Fatalf("listing the captures: %v", err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".xml"))
	}
	return names
}

func TestEachBareDriverHasOneCapture(t *testing.T) {
	var want []string
	for _, b := range Bares {
		want = append(want, b.Name)
	}
	slices.Sort(want)
	got := captureNames(t)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("indi/standard/ holds the captures %v, want one capture for each bare driver, %v; run make captures", got, want)
	}
}

func TestCapturesNameThePinnedINDI(t *testing.T) {
	pinned, err := PinnedINDI(indiPins)
	if err != nil {
		t.Fatalf("reading the INDI pin in %s: %v", indiPins, err)
	}
	for _, name := range captureNames(t) {
		t.Run(name, func(t *testing.T) {
			if got := readCapture(t, name).INDI; got != pinned {
				t.Errorf("the capture is of INDI %s, and indi/package.toml pins %s; run make captures", got, pinned)
			}
		})
	}
}

// The operator detects that a device serves an interface by one
// property of that interface, so each capture must define it.
func TestEachInterfaceDefinesItsDetectionProperty(t *testing.T) {
	cases := []struct {
		capture, property string
	}{
		{"telescope-ra-dec", "EQUATORIAL_EOD_COORD"},
		{"telescope-ha-dec", "EQUATORIAL_EOD_COORD"},
		{"telescope-az-alt", "EQUATORIAL_EOD_COORD"},
		{"telescope-ra-dec-encoder", "EQUATORIAL_EOD_COORD"},
		{"telescope-az-alt-encoder", "EQUATORIAL_EOD_COORD"},
		{"telescope-simple", "EQUATORIAL_EOD_COORD"},
		{"ccd", "CCD_EXPOSURE"},
		{"filter", "FILTER_SLOT"},
		{"focuser", "FOCUS_MOTION"},
		{"focuser-timer", "FOCUS_MOTION"},
		{"rotator", "ABS_ROTATOR_ANGLE"},
		{"dustcap", "CAP_PARK"},
		{"lightbox", "FLAT_LIGHT_CONTROL"},
		{"pac", "PAC_MANUAL_ADJUSTMENT"},
		{"gps", "GPS_REFRESH"},
		{"dome", "DOME_MOTION"},
		{"dome", "DOME_SHUTTER"},
		{"dome-roof", "DOME_MOTION"},
		{"weather", "WEATHER_STATUS"},
		{"output", "DIGITAL_OUTPUT_1"},
		{"input", "DIGITAL_INPUT_1"},
		{"receiver", "RECEIVER_SETTINGS"},
	}
	for _, c := range cases {
		t.Run(c.capture+"/"+c.property, func(t *testing.T) {
			if !readCapture(t, c.capture).Defines(c.property) {
				t.Errorf("the capture of %s defines no %s", c.capture, c.property)
			}
		})
	}
}

// The DefaultDevice capture holds the properties that every device
// defines whatever its interface, so no other capture repeats them.
func TestCapturesLeaveOutTheDefaultDevice(t *testing.T) {
	device := readCapture(t, DefaultDevice)
	for _, name := range captureNames(t) {
		if name == DefaultDevice {
			continue
		}
		t.Run(name, func(t *testing.T) {
			c := readCapture(t, name)
			for _, p := range device.Phases {
				for _, d := range p.Properties {
					if c.Defines(d.Name) {
						t.Errorf("the capture %s defines %s, which only the DefaultDevice capture may define", name, d.Name)
					}
				}
			}
		})
	}
}
