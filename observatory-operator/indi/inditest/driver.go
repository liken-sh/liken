package inditest

import (
	"embed"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
)

// The transcripts are the bytes that a real indiserver sent with one
// simulator behind it: one directory for each simulator, such as ccd,
// and one file for each recorded phase. The package embeds them, so a
// test in another component reads them without a file path into this
// one.
//
//go:embed testdata
var transcripts embed.FS

// Transcript returns one recorded reply. Transcript("ccd", "connect")
// is what indiserver sent when the CCD simulator connected.
func Transcript(simulator, phase string) ([]byte, error) {
	return transcripts.ReadFile(path.Join("testdata", simulator, phase+".xml"))
}

// FrameHeader returns the FITS header cards of a frame that the CCD
// simulator saved while the mount, focuser, rotator, filter wheel, and
// SQM simulators were connected. Each card is 80 columns wide, with the
// trailing spaces removed.
func FrameHeader() []byte {
	data, _ := transcripts.ReadFile("testdata/ccd/frame-header.txt")
	return data
}

// Driver is one simulator behind a fake server. Props are the
// properties it defines now, and it defines OnConnect as well when a
// client connects it.
type Driver struct {
	Device    string
	Props     []*Property
	OnConnect []*Property
	Connected bool
}

// recorded caches the parsed definitions of each simulator's
// transcripts, by simulator name.
var recorded sync.Map

type definitions struct{ baseline, connect []*Property }

// NewDriver returns a disconnected driver for one simulator, such as
// "ccd". It defines what the baseline transcript defines, and defines
// what the connect transcript defines when a client connects it.
func NewDriver(simulator string) (*Driver, error) {
	cached, ok := recorded.Load(simulator)
	if !ok {
		var d definitions
		for _, phase := range []struct {
			name string
			into *[]*Property
		}{{"baseline", &d.baseline}, {"connect", &d.connect}} {
			data, err := Transcript(simulator, phase.name)
			if err != nil {
				return nil, err
			}
			if *phase.into, err = Definitions(data); err != nil {
				return nil, fmt.Errorf("parsing the %s transcript of %s: %w", phase.name, simulator, err)
			}
		}
		cached, _ = recorded.LoadOrStore(simulator, d)
	}
	d := cached.(definitions)
	return DriverOf(d.baseline, d.connect), nil
}

// DriverOf returns a disconnected driver that defines copies of
// baseline, and defines each property of connect that baseline lacks
// when a client connects it. A test uses it to build a driver that no
// simulator provides, such as one device that serves two interfaces.
func DriverOf(baseline, connect []*Property) *Driver {
	d := &Driver{}
	for _, p := range baseline {
		d.Props = append(d.Props, p.Clone())
		d.Device = p.Device
	}
	for _, p := range connect {
		if d.Find(p.Name) == nil {
			d.OnConnect = append(d.OnConnect, p)
		}
	}
	return d
}

// Find returns the property with that name that the driver defines
// now, or nil.
func (d *Driver) Find(name string) *Property {
	for _, p := range d.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Apply sets the property to the values of v and its state to Ok, as a
// simulator does for a change it accepts. It returns the property, or
// nil when the driver does not define it.
func (d *Driver) Apply(v Vector) *Property {
	p := d.Find(v.Name)
	if p == nil {
		return nil
	}
	*p = *p.Updated(v)
	p.State = "Ok"
	return p
}

// Connect connects the driver and defines each property of OnConnect
// that keep accepts, or every one when keep is nil. It returns the
// properties it defined, and nil when the driver is already connected.
func (d *Driver) Connect(keep func(*Property) bool) []*Property {
	if d.Connected {
		return nil
	}
	d.Connected = true
	var added []*Property
	for _, p := range d.OnConnect {
		if keep != nil && !keep(p) {
			continue
		}
		clone := p.Clone()
		d.Props = append(d.Props, clone)
		added = append(added, clone)
	}
	return added
}

// Disconnect disconnects the driver and deletes each property of
// OnConnect. It returns their names, and nil when the driver is not
// connected.
func (d *Driver) Disconnect() []string {
	if !d.Connected {
		return nil
	}
	d.Connected = false
	var deleted []string
	for _, p := range d.OnConnect {
		deleted = append(deleted, p.Name)
		d.Props = slices.DeleteFunc(d.Props, func(o *Property) bool { return o.Name == p.Name })
	}
	return deleted
}

// Connecting reports whether a change to a CONNECTION property turns
// CONNECT on.
func Connecting(p *Property) bool {
	v, _ := p.Value("CONNECT")
	return v == "On"
}

// aborts maps each abort property to the property for the motion that
// it ends.
var aborts = map[string]string{
	"CCD_ABORT_EXPOSURE":     "CCD_EXPOSURE",
	"TELESCOPE_ABORT_MOTION": "EQUATORIAL_EOD_COORD",
}

// Abort returns the properties whose motion an accepted change to p
// ended, with their new state. An abort sets a moving property to Ok,
// as the simulators do. An abort during a park interrupts the park, as
// libindi's telescope does (Telescope::ISNewSwitch in
// libs/indibase/inditelescope.cpp).
func (d *Driver) Abort(p *Property) []*Property {
	var ended []*Property
	if moving, ok := aborts[p.Name]; ok {
		if target := d.Find(moving); target != nil && target.State == "Busy" {
			target.State = "Ok"
			ended = append(ended, target)
		}
	}
	if park := d.Find("TELESCOPE_PARK"); p.Name == "TELESCOPE_ABORT_MOTION" && park != nil && park.State == "Busy" {
		Interrupt(park)
		ended = append(ended, park)
	}
	return ended
}

// Interrupt marks p as libindi's telescope marks TELESCOPE_PARK when an
// abort arrives during a park: it turns every member Off and sets the
// state to Alert.
func Interrupt(p *Property) {
	for i := range p.Members {
		p.Members[i].Value = "Off"
	}
	p.State = "Alert"
}

func key(device, property string) string { return device + "." + property }

// trimmed returns the simulator name for an INDI driver name, such as
// ccd for indi_simulator_ccd.
func trimmed(driver string) string { return strings.TrimPrefix(driver, "indi_simulator_") }
