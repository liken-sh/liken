package machine

// Serio attachments for serial-line devices, such as USB-CEC adapters,
// whose kernel driver binds only after a program attaches the line to
// the kernel's serio layer.
//
// Serio is the kernel's layer for input devices that talk over a byte
// stream. A serio driver binds to a serio port, not to a tty. A serio
// port exists only while a program has set the serport line discipline
// on the tty and is blocked in a read on it, and the kernel removes
// the port when that read returns. On a general-purpose distribution,
// a udev rule starts a service that runs inputattach to do this.
// `liken` has no udev, so init does it (init/serio.go) for each entry
// in spec.serio.
//
// Each row of the protocol table copies the line settings that
// inputattach.c uses for the device, and the serio type from the
// kernel's include/uapi/linux/serio.h. Add a protocol only after you
// test the device on a `liken` machine, because a wrong type binds no
// driver, and a wrong line setting can bind a driver that reads
// garbage.

import (
	"fmt"
	"regexp"
	"slices"
)

// SerioAttachment is one spec.serio entry: a protocol from the table,
// and the USB device whose serial line carries it.
type SerioAttachment struct {
	// Protocol is the Name of a row of the protocol table, such as
	// pulse8-cec.
	Protocol string `json:"protocol"`

	// USB is the adapter that provides the serial line. The entry
	// matches the USB identity and not a tty name, because the kernel
	// numbers ttyACM devices in the order they are plugged in.
	USB SerioUSB `json:"usb"`
}

// SerioUSB is a USB device's identity. Vendor and Product are four
// lowercase hex digits with no prefix, as sysfs writes idVendor and
// idProduct. With Serial, an entry matches only the unit that reports
// that serial number. Without it, the entry matches every device of
// that model, except a line that an entry with a serial also matches,
// because init assigns each line to the entries with a serial first
// (init/serio.go).
type SerioUSB struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Serial  string `json:"serial,omitempty"`
}

// Matches reports whether a USB device matches the entry.
func (a SerioAttachment) Matches(vendor, product, serial string) bool {
	if a.USB.Vendor != vendor || a.USB.Product != product {
		return false
	}
	return a.USB.Serial == "" || a.USB.Serial == serial
}

// String formats the entry for console lines and status messages.
func (a SerioAttachment) String() string {
	s := fmt.Sprintf("%s %s:%s", a.Protocol, a.USB.Vendor, a.USB.Product)
	if a.USB.Serial != "" {
		s += " serial " + a.USB.Serial
	}
	return s
}

// SerioProtocol is one row of the protocol table: the settings that
// init passes to the kernel when it attaches a line, and the modules that
// must be loaded first.
type SerioProtocol struct {
	Name string

	// Type is the serio type from the kernel's serio.h. init sets it
	// on the port with the SPIOCSTYPE ioctl, and the kernel picks the
	// serio driver for the port by this number.
	Type uint8

	// Baud is the line speed in both directions. init also sets 8 data
	// bits and raw mode, for every protocol.
	Baud int

	// LineDriver creates the tty, Discipline registers the serport
	// line discipline, and Driver binds the serio port. A machine must
	// list all three modules in spec.modules.
	LineDriver string
	Discipline string
	Driver     string

	// Adapters lists the USB identities known to use this protocol.
	// When such an adapter has a tty but no spec.serio entry,
	// status.hardware.unclaimed uses this list to name the protocol
	// to declare.
	Adapters []SerioUSB
}

// serioProtocols is the protocol table. Its rows are the USB-CEC
// adapters that the kernel supports, which inputattach attaches with
// `--pulse8-cec` and `--rainshadow-cec`. Type is SERIO_PULSE8_CEC or
// SERIO_RAINSHADOW_CEC.
var serioProtocols = []SerioProtocol{
	{
		Name: "pulse8-cec", Type: 0x40, Baud: 9600,
		LineDriver: "cdc_acm", Discipline: "serport", Driver: "pulse8_cec",
		Adapters: []SerioUSB{{Vendor: "2548", Product: "1002"}},
	},
	{
		Name: "rainshadow-cec", Type: 0x41, Baud: 9600,
		LineDriver: "cdc_acm", Discipline: "serport", Driver: "rainshadow_cec",
	},
}

// LookupSerioProtocol returns the row for a protocol name, or false.
func LookupSerioProtocol(name string) (SerioProtocol, bool) {
	for _, p := range serioProtocols {
		if p.Name == name {
			return p, true
		}
	}
	return SerioProtocol{}, false
}

// SerioProtocolNames returns the protocol names, sorted. The CRD enum
// lists the same names, and a test checks that they match.
func SerioProtocolNames() []string {
	names := make([]string, 0, len(serioProtocols))
	for _, p := range serioProtocols {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

// KnownSerioAdapter returns the name of the protocol whose Adapters
// list the USB identity, or "".
func KnownSerioAdapter(vendor, product string) string {
	for _, p := range serioProtocols {
		for _, adapter := range p.Adapters {
			if adapter.Vendor == vendor && adapter.Product == product {
				return p.Name
			}
		}
	}
	return ""
}

// usbIDPattern matches the form that sysfs uses for idVendor and
// idProduct. serialPattern rejects whitespace and control bytes,
// because the serial is written to a record file in the facts tree,
// and a record file has one line per field.
var (
	usbIDPattern  = regexp.MustCompile(`^[0-9a-f]{4}$`)
	serialPattern = regexp.MustCompile(`^[!-~]{1,126}$`)
)

// ValidateSerio repeats the API server's checks on spec.serio, because
// a manifest on an install stick reaches init without passing through
// the API server.
func ValidateSerio(entries []SerioAttachment) error {
	for i, a := range entries {
		if _, ok := LookupSerioProtocol(a.Protocol); !ok {
			return fmt.Errorf("spec.serio[%d] has protocol %q, and the protocols are %v", i, a.Protocol, SerioProtocolNames())
		}
		if !usbIDPattern.MatchString(a.USB.Vendor) {
			return fmt.Errorf("spec.serio[%d] has vendor %q, and a vendor is four lowercase hex digits, as sysfs spells idVendor", i, a.USB.Vendor)
		}
		if !usbIDPattern.MatchString(a.USB.Product) {
			return fmt.Errorf("spec.serio[%d] has product %q, and a product is four lowercase hex digits, as sysfs spells idProduct", i, a.USB.Product)
		}
		if a.USB.Serial != "" && !serialPattern.MatchString(a.USB.Serial) {
			return fmt.Errorf("spec.serio[%d] has serial %q, and a serial has no space or control byte", i, a.USB.Serial)
		}
		for j := range i {
			if entries[j] == a {
				return fmt.Errorf("spec.serio[%d] and spec.serio[%d] both declare %s, so remove one of them", j, i, a)
			}
		}
	}
	return nil
}

// SerioState is the state of one attachment on the machine.
//
// Attached means init is blocked in the read, the port exists, and a
// driver is bound to it. Missing means the entry has no serial line:
// the adapter is unplugged, its line driver is not loaded, or another
// entry took every matching line. Refused covers every other failure,
// including a protocol that this release does not have, a module that
// is not loaded, an error from the kernel, and a port that no driver
// has bound yet.
type SerioState string

const (
	SerioAttached SerioState = "Attached"
	SerioMissing  SerioState = "Missing"
	SerioRefused  SerioState = "Refused"
)

// SerioStatus reports one serial line that init attached or tried to
// attach. An entry that matches no line has one Missing status, and an
// entry that matches several lines has one status for each line.
type SerioStatus struct {
	Protocol string   `json:"protocol"`
	USB      SerioUSB `json:"usb"`

	// TTY is the serial line that init matched, such as ttyACM0, and
	// Port is the serio port that the kernel registered on it, such as
	// serio0.
	TTY  string `json:"tty,omitempty"`
	Port string `json:"port,omitempty"`

	State SerioState `json:"state"`

	// Message explains a Missing or Refused state, such as the
	// kernel's error text or the module to add to spec.modules.
	Message string `json:"message,omitempty"`

	// Nodes lists the device nodes that the driver created for the
	// port, such as /dev/cec0 and the remote's event node.
	Nodes []string `json:"nodes,omitempty"`
}

// Attachment returns the spec.serio entry that this status describes.
func (s SerioStatus) Attachment() SerioAttachment {
	return SerioAttachment{Protocol: s.Protocol, USB: s.USB}
}
