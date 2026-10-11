package main

// Each device kind is one INDI interface, and what the operator does
// with a device follows from the interface: which property shows that
// the device serves it, which switch reaches each state, and which
// property aborts its motion. A resource acts through the INDI device
// that serves it (served.go), so a device that serves two interfaces,
// such as a Flip-Flat that serves a DustCap and a FlatPanel, takes the
// actions of each through the resource of each.
//
// A device serves an interface when it defines one of the interface's
// detection properties after it connects. DRIVER_INTERFACE in
// DRIVER_INFO is a hint only: a QHY camera sets FILTER only when it
// finds a wheel at connect, an SBIG camera sets it with no wheel, and a
// Receiver and an INDI::Spectrograph set the same bit. The properties
// answer each of those cases.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// iface is what the operator does with one INDI interface.
type iface struct {
	// states maps each state of the kind's state action to the switch
	// that reaches it.
	states map[string]switchTarget
	// abort is the property that ends the interface's motion, and
	// watched the property that is Busy while it moves.
	abort, watched string
	// cooler is true for an interface whose cool and warm actions
	// drive CCD_TEMPERATURE and CCD_COOLER.
	cooler bool
	// locks is true for an interface whose park the park locks
	// decide, so the operator relays the locks before a move.
	locks bool
}

// interfaces holds what the operator does with each interface, by the
// kind that is the interface. A kind that is absent has no action of
// its own.
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

// propertyReader is what an INDI client or a store holds of the
// properties that the devices define.
type propertyReader interface {
	Property(device, name string) (indi.Property, bool)
}

// serves reports whether a resource's INDI device defines one of the
// detection properties of the resource's interface now.
func serves(c propertyReader, device string, kind observatory.Kind) bool {
	for _, name := range observatory.Standards[kind].Detect {
		if _, ok := c.Property(device, name); ok {
			return true
		}
	}
	return false
}

// missingInterface is the fault of a connected device that does not
// serve its resource's interface.
func missingInterface(h handle) error {
	return fmt.Errorf("INDI device %s defines no %s after it connects, so it serves no %s",
		h.name, strings.Join(observatory.Standards[h.d.kind].Detect, " or "), h.d.kind.Name)
}

// detect decides whether a connected device serves the interface of a
// resource on it, and records the verdict. The properties of an
// interface arrive in the burst that follows CONNECTION, and the dust
// cover simulator defines CAP_PARK just after CONNECTION, so a device
// that defines none of them yet may still be on its way. detect then
// waits for the driver to answer a getProperties (indi.Client.Sync),
// which it does after everything it wrote before, and decides from
// what the device defines then. ctx bounds that wait, and a wait that
// ends with no answer decides nothing. A resource whose device does
// not serve its interface stays connected, because the other resources
// that its driver serves still need it, and the steps leave it out.
func (o *operator) detect(ctx context.Context, h handle) bool {
	if !h.serving() {
		if err := h.client().Sync(ctx, h.name); err != nil {
			return false
		}
	}
	serving := h.serving()
	o.mu.Lock()
	defer o.mu.Unlock()
	if serving {
		delete(o.lacking, h.d.key())
	} else {
		o.lacking[h.d.key()] = missingInterface(h).Error()
	}
	return serving
}

// lackingOf answers why a resource's device does not serve its
// interface, by the last verdict, or "" when it serves it or has no
// verdict.
func (o *operator) lackingOf(d *device) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lacking[d.key()]
}

// serving reports whether a handle's device serves its resource's
// interface now.
func (h handle) serving() bool { return serves(h.client(), h.name, h.d.kind) }

// judgeInterfaces keeps the verdict of each connected device on a
// server current, on each change that the runner wakes for. A device
// with no verdict, such as one the operator finds after a restart, is
// judged once (detect). A resource whose device defines its
// interface's properties after a verdict that it lacked them heals:
// it gets its settings, once, and its activation runs as a resource's
// that joins a Ready session (joins.go), because the Activation step
// left it out.
func (r *runner) judgeInterfaces(ctx context.Context, t *tree, ref serverRef) {
	_, site, err := r.siteOf(t)
	if err != nil {
		return
	}
	// A driver that never answers holds the runner no longer than the
	// work of a device that came back may take. It is a deadline.
	ctx, cancel := context.WithTimeout(ctx, reapplyLimit)
	defer cancel()
	all := r.o.handlesOf(t, ref, t.devicesOn(ref))
	for _, h := range all {
		if !h.connected() {
			continue
		}
		lacked := r.o.lackingOf(h.d) != ""
		switch {
		case lacked && h.serving():
			r.o.detect(ctx, h)
			var notes []string
			_, err := configureDevice(ctx, t, site, h, all, &notes)
			r.o.fault(h.d, err)
			r.o.logf("%s now serves its interface on %s", h, ref)
		case !lacked && !h.serving():
			r.o.detect(ctx, h)
		}
	}
}
