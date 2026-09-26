package hardware

// The unclaimed report's one entry for a device that has a driver.
//
// A USB-CEC adapter shows why the report needs it. Before cdc_acm
// binds the adapter, the ordinary report lists it and names cdc_acm.
// After cdc_acm binds it, the adapter has a driver and a serial line,
// and it still does nothing: its real driver is a serio driver, which
// binds only after init attaches the line (machine/serio.go). The
// ordinary rule drops a driven device, so without this entry the
// adapter would leave the report at the moment it is half done.
//
// The entry is narrow on purpose. It lists only an identity that the
// protocol table names as a known adapter, only the interface that
// owns the serial line, and only while no spec.serio entry matches
// the device. Any other driven device is working equipment.

import (
	"fmt"

	"github.com/liken-sh/liken/machine"
)

// cdcCommunicationsClass is the USB interface class of a CDC
// communications interface. A CDC ACM device has two interfaces that
// cdc_acm binds: the communications interface, which owns the tty,
// and the data interface beside it. The report lists the first, so
// one adapter is one entry.
const cdcCommunicationsClass = "02"

// unattachedAdapter reports the entry for one driven device, when the
// device is a known serial-line adapter that no spec.serio entry
// attaches.
func unattachedAdapter(d Device, serio []machine.SerioAttachment) (machine.UnclaimedDevice, bool) {
	if d.Bus != "usb" || d.ClassCode != cdcCommunicationsClass {
		return machine.UnclaimedDevice{}, false
	}
	protocol, ok := machine.LookupSerioProtocol(machine.KnownSerioAdapter(d.Vendor, d.Product))
	if !ok || d.Driver != protocol.LineDriver {
		return machine.UnclaimedDevice{}, false
	}
	for _, entry := range serio {
		if entry.Matches(d.Vendor, d.Product, d.Serial) {
			return machine.UnclaimedDevice{}, false
		}
	}
	return machine.UnclaimedDevice{
		Modalias: d.Modalias,
		Bus:      d.Bus,
		Name:     d.Name,
		Class:    d.Class,
		// The entry names no candidates. The alias table did not put
		// it here, and the fix is two modules and a spec.serio entry
		// together, which the message states whole.
		Message: fmt.Sprintf("declare %s and %s in spec.modules and a %s entry in spec.serio",
			protocol.Discipline, protocol.Driver, protocol.Name),
	}, true
}
