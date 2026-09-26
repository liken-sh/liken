package machine

// Serio attachments: the serial-line devices whose kernel driver binds
// only after a program attaches the line to the kernel's serio layer.
//
// Serio is the kernel's layer for input devices that talk over a byte
// stream: old serial mice, touchscreens, and USB-CEC adapters. A serio
// driver binds to a serio port, not to a tty, and a serio port exists
// only while a program holds the serport line discipline on the tty
// and blocks in a read on it. The kernel unregisters the port when
// that read returns. On a general-purpose distribution, udev starts a
// service that runs inputattach to hold the read. liken has neither,
// so init holds it (init/serio.go), for each entry in spec.serio.
//
// This file is the one piece of device knowledge liken keeps for
// these devices: the protocol table. Each name maps to the values
// that inputattach.c uses for the same device, and the serio type
// comes from the kernel's include/uapi/linux/serio.h. A name joins
// the table only when somebody examines that device on a liken
// machine, because a wrong type binds no driver, and a wrong line
// setting can bind one that reads garbage.

import (
	"fmt"
	"regexp"
	"slices"
)

// SerioAttachment is one spec.serio entry: a protocol from the table,
// and the USB device whose serial line carries it.
type SerioAttachment struct {
	// Protocol names a row of the protocol table, for example
	// pulse8-cec.
	Protocol string `json:"protocol"`

	// USB is the identity of the hardware that carries the serial
	// line. The match uses this identity, not a tty name, because the
	// kernel numbers ttyACM0 and ttyACM1 in the order the devices were
	// plugged in.
	USB SerioUSB `json:"usb"`
}

// SerioUSB is a USB device's identity, in the spelling the DRA driver
// publishes as the vendor and product attributes: four lowercase hex
// digits each, with no prefix. Serial is optional. With it, the entry
// matches only the unit that reports that serial number. Without it,
// the entry matches every device of that model, and the serio walk
// gives it only the lines that no entry with a serial took
// (init/serio.go).
type SerioUSB struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Serial  string `json:"serial,omitempty"`
}

// Matches reports whether a USB device's identity is the one this
// entry declares.
func (a SerioAttachment) Matches(vendor, product, serial string) bool {
	if a.USB.Vendor != vendor || a.USB.Product != product {
		return false
	}
	return a.USB.Serial == "" || a.USB.Serial == serial
}

// String names the entry for a console line or a status message.
func (a SerioAttachment) String() string {
	s := fmt.Sprintf("%s %s:%s", a.Protocol, a.USB.Vendor, a.USB.Product)
	if a.USB.Serial != "" {
		s += " serial " + a.USB.Serial
	}
	return s
}

// SerioProtocol is one row of the protocol table: the values init
// passes to the kernel for the attach, and the modules the attach
// depends on.
type SerioProtocol struct {
	Name string

	// Type is the serio type from the kernel's serio.h, which
	// SPIOCSTYPE hands to serport. The kernel matches serio drivers
	// to ports by this number, so it is what makes pulse8_cec, and no
	// other driver, bind the port.
	Type uint8

	// Baud is the line speed, in both directions. Every row today
	// uses 9600 baud, 8 data bits, and raw mode, which is what the
	// adapters' firmware speaks.
	Baud int

	// LineDriver is the module that creates the serial line, and
	// Discipline is the module that registers the serport line
	// discipline. Driver is the serio driver that binds the port.
	// The attach needs all three loaded, and spec.modules is where a
	// machine declares them.
	LineDriver string
	Discipline string
	Driver     string

	// Adapters lists the USB identities known to speak this protocol.
	// The unclaimed report uses the list to name the rest of the fix
	// for an adapter that a line driver binds but no entry attaches.
	Adapters []SerioUSB
}

// serioProtocols is the table. The two rows are the two USB-CEC
// adapters the kernel supports. inputattach runs both as
// `--pulse8-cec` and `--rainshadow-cec`, at 9600 baud, with the serio
// types SERIO_PULSE8_CEC and SERIO_RAINSHADOW_CEC.
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

// LookupSerioProtocol returns the table's row for a protocol name.
func LookupSerioProtocol(name string) (SerioProtocol, bool) {
	for _, p := range serioProtocols {
		if p.Name == name {
			return p, true
		}
	}
	return SerioProtocol{}, false
}

// SerioProtocolNames lists the table's names, sorted. The CRD's enum
// holds the same list, and a test keeps the two equal.
func SerioProtocolNames() []string {
	names := make([]string, 0, len(serioProtocols))
	for _, p := range serioProtocols {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

// KnownSerioAdapter returns the protocol a USB identity is known to
// speak, or "" for an identity the table does not list.
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

// usbIDPattern is the spelling sysfs uses for idVendor and idProduct.
// serialPattern refuses whitespace and control bytes, because the
// serial reaches a record file in the facts tree, whose grammar is one
// line per field.
var (
	usbIDPattern  = regexp.MustCompile(`^[0-9a-f]{4}$`)
	serialPattern = regexp.MustCompile(`^[!-~]{1,126}$`)
)

// ValidateSerio checks the entries the API server also checks, for
// the manifests that reach a machine without it: a manifest carried
// in on a stick was never admitted. An entry that fails here could
// never match a device, or would match one twice.
func ValidateSerio(entries []SerioAttachment) error {
	for i, a := range entries {
		if _, ok := LookupSerioProtocol(a.Protocol); !ok {
			return fmt.Errorf("serio entry %d names protocol %q; the protocols are %v", i, a.Protocol, SerioProtocolNames())
		}
		if !usbIDPattern.MatchString(a.USB.Vendor) {
			return fmt.Errorf("serio entry %d declares vendor %q; a vendor is four lowercase hex digits, as sysfs spells idVendor", i, a.USB.Vendor)
		}
		if !usbIDPattern.MatchString(a.USB.Product) {
			return fmt.Errorf("serio entry %d declares product %q; a product is four lowercase hex digits, as sysfs spells idProduct", i, a.USB.Product)
		}
		if a.USB.Serial != "" && !serialPattern.MatchString(a.USB.Serial) {
			return fmt.Errorf("serio entry %d declares serial %q; a serial holds no space or control byte", i, a.USB.Serial)
		}
		for j := range i {
			if entries[j] == a {
				return fmt.Errorf("serio entries %d and %d both declare %s; declare each attachment once", j, i, a)
			}
		}
	}
	return nil
}

// SerioState is one attachment's standing on the machine.
//
// Attached means init holds the read, and the port exists. Missing
// means no serial line matches the entry: the adapter is unplugged, or
// its line driver is not loaded. Refused means a line matches and the
// attach did not complete: a module the attach needs is not loaded,
// or the kernel refused one of the calls.
type SerioState string

const (
	SerioAttached SerioState = "Attached"
	SerioMissing  SerioState = "Missing"
	SerioRefused  SerioState = "Refused"
)

// SerioStatus is one attachment that init holds or tried to hold. An
// entry that matches no line reports once, as Missing. An entry that
// matches two lines, because it declares no serial and two identical
// adapters are plugged in, reports once for each line.
type SerioStatus struct {
	Protocol string   `json:"protocol"`
	USB      SerioUSB `json:"usb"`

	// TTY is the serial line init matched, for example ttyACM0, and
	// Port is the serio port the kernel registered on it, for example
	// serio0.
	TTY  string `json:"tty,omitempty"`
	Port string `json:"port,omitempty"`

	State SerioState `json:"state"`

	// Message gives the cause of a Missing or Refused state, with the
	// kernel's error text word for word, or the module to declare.
	Message string `json:"message,omitempty"`

	// Nodes lists the device nodes the attached driver created under
	// the port, for example /dev/cec0 and the remote's event node.
	Nodes []string `json:"nodes,omitempty"`
}

// Attachment returns the spec entry a status reports on.
func (s SerioStatus) Attachment() SerioAttachment {
	return SerioAttachment{Protocol: s.Protocol, USB: s.USB}
}
