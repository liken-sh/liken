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
// simulator behind it, one directory for each simulator, such as ccd
// or telescope, and one file for each message that the recorder sent.
// The package embeds them, so a test of another component reads them
// with no path into this one.
//
//go:embed testdata
var transcripts embed.FS

// Transcript answers one recorded reply, such as Transcript("ccd",
// "connect"), the bytes that indiserver sent when the CCD simulator
// connected.
func Transcript(simulator, phase string) ([]byte, error) {
	return transcripts.ReadFile(path.Join("testdata", simulator, phase+".xml"))
}

// FrameHeader answers the 80-column header cards, with trailing spaces
// removed, of a frame that the CCD simulator saved with the mount,
// focuser, rotator, filter wheel, and SQM simulators connected.
func FrameHeader() []byte {
	data, _ := transcripts.ReadFile("testdata/ccd/frame-header.txt")
	return data
}

// Driver is one simulator behind a fake server. It defines Props now,
// and adds OnConnect when a client connects it.
type Driver struct {
	Device    string
	Props     []*Property
	OnConnect []*Property
	Connected bool
}

// recorded holds what each simulator's transcripts define, parsed
// once, by simulator.
var recorded sync.Map

type definitions struct{ baseline, connect []*Property }

// NewDriver answers a disconnected driver of one simulator, such as
// "ccd", that defines what its baseline transcript defines, and adds
// what its connect transcript defines when it connects.
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
				return nil, fmt.Errorf("%s/%s: %w", simulator, phase.name, err)
			}
		}
		cached, _ = recorded.LoadOrStore(simulator, d)
	}
	d := cached.(definitions)
	return DriverOf(d.baseline, d.connect), nil
}

// DriverOf answers a disconnected driver that defines copies of
// baseline, and adds copies of each property of connect that baseline
// lacks when it connects. A test builds a driver that no simulator is
// this way, such as one device that serves two interfaces.
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

// Find answers the property of a name that the driver defines now, or
// nil.
func (d *Driver) Find(name string) *Property {
	for _, p := range d.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Apply takes the values of a change and sets the state Ok, as a
// simulator answers a change it accepts. It answers the property, or
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
// that keep accepts, or every one when keep is nil. It answers the
// properties it defined, and nothing when the driver is connected.
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
// OnConnect. It answers their names, and nothing when the driver is
// not connected.
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

// Connecting answers whether a CONNECTION property asks to connect.
func Connecting(p *Property) bool {
	v, _ := p.Value("CONNECT")
	return v == "On"
}

// aborts maps each abort property to the property whose motion it
// ends.
var aborts = map[string]string{
	"CCD_ABORT_EXPOSURE":     "CCD_EXPOSURE",
	"TELESCOPE_ABORT_MOTION": "EQUATORIAL_EOD_COORD",
}

// Abort answers the properties whose motion an accepted change to p
// ended, with their new state. An abort ends what moves, as the
// simulators' abort does, and an abort during a park ends the park,
// as libindi's telescope does (Telescope::ISNewSwitch in
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

// Interrupt answers a change to a property that moves, as libindi's
// telescope answers TELESCOPE_PARK during a park: it aborts the
// motion, turns every switch Off, and sets Alert.
func Interrupt(p *Property) {
	for i := range p.Members {
		p.Members[i].Value = "Off"
	}
	p.State = "Alert"
}

// key names one property of one device, as "<device>.<property>".
func key(device, property string) string { return device + "." + property }

// trimmed answers the simulator's name for an INDI driver, such as ccd
// for indi_simulator_ccd.
func trimmed(driver string) string { return strings.TrimPrefix(driver, "indi_simulator_") }
