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
	"slices"
	"strings"
	"time"

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

// verdict is the operator's last decision on whether a resource's INDI
// device serves the resource's interface. It caches what the device
// defined when the operator last looked, and what the operator wrote
// to it since, for the current connection of the driver.
type verdict struct {
	// lacking says why the device does not serve the interface, or is
	// "" while it does.
	lacking string
	// silent is true when the driver did not answer a request for its
	// properties (indi.Client.Sync), and connection is the revision of
	// the device's CONNECTION then. The operator asks again only after
	// CONNECTION changes, because each request makes the driver define
	// every property again, and indiserver sends that burst to every
	// client.
	silent     bool
	connection uint64
	// unwritten is true while the resource's settings have not gone to
	// the device, because the device did not serve the interface when
	// the operator would have written them (configureDevice). The
	// settings go out once for each connection of the driver, so a
	// device that stops defining the interface for a while and defines
	// it again keeps what a client such as a sequencer changed.
	unwritten bool
}

// syncLimit bounds the wait for a driver to answer a request for its
// properties. A driver answers within a second, so one that has not
// answered in this time does not answer. It is a deadline.
const syncLimit = 30 * time.Second

// detect decides whether a connected device serves the interface of a
// resource on it, and records the verdict. The properties of an
// interface arrive in the burst that follows CONNECTION, and the dust
// cover simulator defines CAP_PARK just after CONNECTION, so a device
// that defines none of them yet may still be on its way. detect then
// waits for the driver to answer a getProperties (indi.Client.Sync),
// which it does after everything it wrote before, and decides from
// what the device defines then. A driver that does not answer within
// syncLimit gets a verdict that says so. A resource whose device does
// not serve its interface stays connected, because the other resources
// that its driver serves still need it, and the steps leave it out.
func (o *operator) detect(ctx context.Context, h handle) bool {
	if !h.serving() {
		wait, cancel := context.WithTimeout(ctx, syncLimit)
		err := h.client().Sync(wait, h.name)
		cancel()
		if err != nil {
			// A caller that stops records nothing about the driver.
			if ctx.Err() == nil {
				o.judge(h.d, false, verdict{silent: true, connection: h.client().Revision(h.name, "CONNECTION"),
					lacking: fmt.Sprintf("INDI device %s did not answer a request for its properties: %v", h.name, err)})
			}
			return false
		}
	}
	serving := h.serving()
	v := verdict{}
	if !serving {
		v.lacking = missingInterface(h).Error()
	}
	o.judge(h.d, serving, v)
	return serving
}

// judge records a verdict on a resource. The first verdict on a
// resource since the operator started marks its settings unwritten
// when the device does not serve the interface, because the operator
// cannot tell whether they went out before. Every later verdict keeps
// the mark of the one before.
func (o *operator) judge(d *device, serving bool, v verdict) {
	o.mu.Lock()
	defer o.mu.Unlock()
	before, judged := o.verdicts[d.key()]
	v.unwritten = before.unwritten || (!judged && !serving)
	o.verdicts[d.key()] = v
}

// written records whether a resource's settings went to its device.
func (o *operator) written(d *device, sent bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.verdicts[d.key()]
	v.unwritten = !sent
	o.verdicts[d.key()] = v
}

// verdictOf answers the last verdict on a resource, and false when it
// has none.
func (o *operator) verdictOf(d *device) (verdict, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.verdicts[d.key()]
	return v, ok
}

// lackingOf answers why a resource's device does not serve its
// interface, by the last verdict, or "" when it serves it or has no
// verdict.
func (o *operator) lackingOf(d *device) string {
	v, _ := o.verdictOf(d)
	return v.lacking
}

// serving reports whether a handle's device serves its resource's
// interface now.
func (h handle) serving() bool { return serves(h.client(), h.name, h.d.kind) }

// judgeInterfaces keeps the verdict of each connected device on a
// server current, on each change that the runner wakes for:
//
//   - A device with no verdict, such as one the operator finds after a
//     restart, is judged once (detect), and so is a device that stops
//     defining its interface's properties.
//   - A device whose driver did not answer is asked again when its
//     CONNECTION changes, and not before.
//   - A resource whose device defines its interface's properties after
//     a verdict that it lacked them heals. It gets the settings that
//     did not go out, and its activation runs as a resource's that
//     joins a Ready session (joins.go), because the Activation step
//     left it out.
//
// The work holds the server's lock, and the site's for the
// observatory's server, which the runners of several telescopes share
// (holding). Another runner may have judged a device while this one
// waited, so each device is checked again under the locks.
func (r *runner) judgeInterfaces(ctx context.Context, t *tree, ref serverRef) {
	_, site, err := r.siteOf(t)
	if err != nil {
		return
	}
	all := r.o.handlesOf(t, ref, t.devicesOn(ref))
	if !slices.ContainsFunc(all, r.o.needsJudging) {
		return
	}
	err = holding(ctx, r.o.locksOf(ref, all[0].server), func(ctx context.Context) error {
		for _, h := range all {
			if !r.o.needsJudging(h) {
				continue
			}
			before, _ := r.o.verdictOf(h.d)
			if !r.o.detect(ctx, h) {
				continue
			}
			if before.unwritten {
				var notes []string
				_, err := r.o.configureDevice(ctx, t, site, h, all, &notes)
				r.o.fault(h.d, err)
			}
			if before.lacking != "" {
				r.o.logf("%s now serves its interface on %s", h, ref)
			}
		}
		return nil
	})
	if err != nil {
		r.o.logf("judging the interfaces on %s: %v", ref, err)
	}
}

// needsJudging reports whether a connected device's verdict is stale
// (judgeInterfaces).
func (o *operator) needsJudging(h handle) bool {
	if !h.connected() {
		return false
	}
	v, judged := o.verdictOf(h.d)
	serving := h.serving()
	switch {
	case serving:
		return v.lacking != "" || v.unwritten
	case v.silent:
		return h.client().Revision(h.name, "CONNECTION") != v.connection
	}
	return !judged || v.lacking == ""
}
