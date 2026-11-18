package main

// Each device kind maps to one INDI interface, which gives the switch
// that moves a device to each state and the property that aborts its
// motion. One INDI device can implement two interfaces, such as a
// Flip-Flat that is a dust cap and a flat panel. Then a resource of
// each kind acts on the same device.
//
// The operator treats a device as implementing an interface when the
// device defines one of the interface's detection properties after it
// connects. It does not trust the DRIVER_INTERFACE bits in
// DRIVER_INFO: a QHY camera sets the filter wheel bit only when it
// finds a wheel at connect, an SBIG camera sets it when it has no
// wheel, and a receiver and an INDI::Spectrograph set the same bit.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type iface struct {
	// states maps each value of the state action to the switch that
	// sets it.
	states map[string]switchTarget
	// abort is the property that stops the device's motion, and
	// watched is the property that is Busy while it moves.
	abort, watched string
	// cooler is true when the cool and warm actions drive
	// CCD_TEMPERATURE and CCD_COOLER.
	cooler bool
	// locks is true for a mount and a dome, whose drivers can refuse a
	// park or an unpark because of the other's park state. Before a
	// state change, the operator sends each driver the current park
	// states (locks.go).
	locks bool
}

// A kind that is not in interfaces has no actions of its own.
var interfaces = map[observatory.Kind]iface{
	observatory.DomeKind: {states: map[string]switchTarget{
		string(observatory.StateParked):   {"DOME_PARK", "PARK", "parking", "parked", "parked"},
		string(observatory.StateUnparked): {"DOME_PARK", "UNPARK", "unparking", "unparked", "unparked"},
	}, locks: true},
	observatory.MountKind: {states: map[string]switchTarget{
		string(observatory.StateParked):   {"TELESCOPE_PARK", "PARK", "parking", "parked", "parked"},
		string(observatory.StateUnparked): {"TELESCOPE_PARK", "UNPARK", "unparking", "unparked", "unparked"},
	}, abort: "TELESCOPE_ABORT_MOTION", watched: "EQUATORIAL_EOD_COORD", locks: true},
	observatory.DustCapKind: {states: map[string]switchTarget{
		string(observatory.StateOpen):   {"CAP_PARK", "UNPARK", "opening", "opened", "open"},
		string(observatory.StateClosed): {"CAP_PARK", "PARK", "closing", "closed", "closed"},
	}},
	observatory.FlatPanelKind: {states: map[string]switchTarget{
		string(observatory.StateLit):  {"FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON", "switching on the light of", "switched on the light of", "lit"},
		string(observatory.StateDark): {"FLAT_LIGHT_CONTROL", "FLAT_LIGHT_OFF", "switching off the light of", "switched off the light of", "dark"},
	}},
	observatory.CameraKind: {abort: "CCD_ABORT_EXPOSURE", watched: "CCD_EXPOSURE", cooler: true},
}

// propertyReader reads the properties that devices have defined, from
// an INDI client or from a store.
type propertyReader interface {
	Property(device, name string) (indi.Property, bool)
}

func serves(c propertyReader, device string, kind observatory.Kind) bool {
	for _, name := range observatory.Standards[kind].Detect {
		if _, ok := c.Property(device, name); ok {
			return true
		}
	}
	return false
}

func missingInterface(h handle) error {
	return fmt.Errorf("INDI device %s defines no %s after it connects, so it serves no %s",
		h.name, strings.Join(observatory.Standards[h.d.kind].Detect, " or "), h.d.kind.Name)
}

// interfaceCheck is the result of the last check of whether a resource's INDI
// device implements the resource's interface.
type interfaceCheck struct {
	// lacking is the reason the device does not implement the
	// interface, or "" when it does.
	lacking string
	// silent is true when the driver did not answer a request for its
	// properties, and connection is then the revision of the device's
	// CONNECTION property. The operator sends the request again only
	// after CONNECTION changes, because each request makes the driver
	// define every property again, and indiserver sends all of them to
	// every client.
	silent     bool
	connection uint64
	// unwritten is true when the resource's settings may not be on the
	// device, because the device did not implement the interface when
	// configureDevice ran, or at the first check after the operator
	// started (recordCheck). checkInterfaces writes the settings when the
	// interface appears only while unwritten is true. So a device that
	// drops its interface and defines it again keeps the values that a
	// client such as a sequencer set in the meantime.
	unwritten bool
}

// syncLimit is how long detect waits for a driver to answer a request
// for its properties. A driver normally answers within a second.
const syncLimit = 30 * time.Second

// detect checks whether a connected device implements its resource's
// interface, records the check, and returns the result. A driver
// defines an interface's properties in the burst that follows
// CONNECTION, and the dust cap simulator defines CAP_PARK slightly
// after that burst. So when the device defines none of them yet,
// detect sends a getProperties and waits for the driver's answer
// (indi.Client.Sync), which arrives after everything the driver sent
// before. A device that does not implement its interface stays
// connected, because other resources on the same driver still need
// the connection, and the steps skip its resource.
func (o *operator) detect(ctx context.Context, h handle) bool {
	if !h.serving() {
		wait, cancel := context.WithTimeout(ctx, syncLimit)
		err := h.client().Sync(wait, h.name)
		cancel()
		if err != nil {
			// A failure caused by the caller's cancel says nothing
			// about the driver, so record no check.
			if ctx.Err() == nil {
				o.recordCheck(h.d, false, interfaceCheck{silent: true, connection: h.client().Revision(h.name, "CONNECTION"),
					lacking: fmt.Sprintf("INDI device %s did not answer a request for its properties: %v", h.name, err)})
			}
			return false
		}
	}
	serving := h.serving()
	v := interfaceCheck{}
	if !serving {
		v.lacking = missingInterface(h).Error()
	}
	o.recordCheck(h.d, serving, v)
	return serving
}

// recordCheck records a check and keeps unwritten from the check
// before. When the first check after the operator starts finds no
// interface, recordCheck sets unwritten, because the operator cannot tell
// whether its previous process wrote the settings.
func (o *operator) recordCheck(d *device, serving bool, v interfaceCheck) {
	o.mu.Lock()
	defer o.mu.Unlock()
	before, checked := o.interfaceChecks[d.key()]
	v.unwritten = before.unwritten || (!checked && !serving)
	o.interfaceChecks[d.key()] = v
}

func (o *operator) written(d *device, sent bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.interfaceChecks[d.key()]
	v.unwritten = !sent
	o.interfaceChecks[d.key()] = v
}

func (o *operator) interfaceCheckOf(d *device) (interfaceCheck, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.interfaceChecks[d.key()]
	return v, ok
}

// lackingOf returns "" when there is no check yet.
func (o *operator) lackingOf(d *device) string {
	v, _ := o.interfaceCheckOf(d)
	return v.lacking
}

func (h handle) serving() bool { return serves(h.client(), h.name, h.d.kind) }

// checkInterfaces checks the connected devices on a server again each
// time the runner's steady loop wakes, for the cases that
// needsInterfaceCheck lists. When a device that lacked its interface now
// implements it, checkInterfaces writes its unwritten settings, and the
// trigger controller runs the activation that the Activation step
// skipped (lateactivation.go).
//
// The checks run under the server's lock, and for the observatory's
// server also under the site's lock (locksOf). Another runner can check
// a device while this one waits for the locks, so needsInterfaceCheck runs
// again once the locks are held.
func (r *runner) checkInterfaces(ctx context.Context, t *tree, ref serverRef) {
	_, site, err := r.siteOf(t)
	if err != nil {
		return
	}
	all := r.o.handlesOf(t, ref, t.devicesOn(ref))
	if !slices.ContainsFunc(all, r.o.needsInterfaceCheck) {
		return
	}
	err = withLocks(ctx, r.o.locksOf(ref, all[0].server), func(ctx context.Context) error {
		for _, h := range all {
			if !r.o.needsInterfaceCheck(h) {
				continue
			}
			before, _ := r.o.interfaceCheckOf(h.d)
			if !r.o.detect(ctx, h) {
				continue
			}
			if before.unwritten {
				var notes []string
				_, err := r.o.configureDevice(ctx, t, site, h, all, &notes)
				r.o.fault(h.d, err)
			}
			if before.lacking != "" {
				r.o.logf("%s now implements its interface on %s", h, ref)
			}
		}
		return nil
	})
	if err != nil {
		r.o.logf("checking the interfaces on %s: %v", ref, err)
	}
}

// needsInterfaceCheck reports whether a connected device needs a new check: a
// device without its interface and with no check, such as one the
// operator finds after a restart, a device that gained or lost its
// interface since its check, a serving device whose settings are
// unwritten, and a silent driver whose CONNECTION changed.
func (o *operator) needsInterfaceCheck(h handle) bool {
	if !h.connected() {
		return false
	}
	v, checked := o.interfaceCheckOf(h.d)
	serving := h.serving()
	switch {
	case serving:
		return v.lacking != "" || v.unwritten
	case v.silent:
		return h.client().Revision(h.name, "CONNECTION") != v.connection
	}
	return !checked || v.lacking == ""
}
