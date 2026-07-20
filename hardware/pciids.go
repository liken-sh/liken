package hardware

// pci.ids is the naming database for PCI hardware.
//
// USB devices carry their own names: manufacturer and product
// strings that the kernel reads at enumeration. PCI devices carry
// only numbers. The names that lspci prints come from a community
// database, pci.ids, maintained by the hwdata project. liken vendors
// this database as a pinned flat file, and the hwdata domain owns
// the pin. Because of this, an unclaimed-device report can say "Red
// Hat, Inc. Virtio 1.0 GPU" instead of "1af4:1050". This dependency
// is soft by design. Every caller falls back to the numeric IDs when
// the file is missing, because naming adds convenience, and
// reporting is the required task.

import (
	"os"
	"strings"
)

// PCIIDs is the loaded database. It indexes vendors by their 4-digit
// hex ID, and devices by vendor plus device. This code does not
// parse the file's third level, the subsystem names, or its
// trailing class-code section. The report names devices, and class
// words come from the spec-defined table in names.go.
type PCIIDs struct {
	vendors map[string]string
	devices map[string]string
}

// LoadPCIIDs parses the pci.ids format. Vendor lines start at the
// left margin (for example, "1af4  Red Hat, Inc."). Device lines
// start one tab in. Subsystem lines start two tabs in. Single-letter
// sections (for example, "C 03  Display controller") come after the
// vendor lines.
func LoadPCIIDs(path string) (*PCIIDs, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ids := &PCIIDs{vendors: map[string]string{}, devices: map[string]string{}}
	vendor := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "\t\t"):
			// Subsystem names give more detail than a status line needs.
		case strings.HasPrefix(line, "\t"):
			if id, name, found := strings.Cut(line[1:], "  "); found && vendor != "" {
				ids.devices[vendor+":"+id] = name
			}
		default:
			id, name, found := strings.Cut(line, "  ")
			// A non-hex first field, such as the "C 03" class
			// sections, ends the vendor list for this parser.
			if !found || len(id) != 4 || strings.ContainsFunc(id, notHex) {
				vendor = ""
				continue
			}
			vendor = strings.ToLower(id)
			ids.vendors[vendor] = name
		}
	}
	return ids, nil
}

func notHex(r rune) bool {
	return !strings.ContainsRune("0123456789abcdefABCDEF", r)
}

// Name renders a vendor:device pair in words. It returns both names
// when the database knows the device. It returns the vendor name
// plus the numeric device ID when the database knows only the
// vendor. It returns nothing for an unknown vendor, because the
// caller's numeric fallback reads better than a bare hex device ID
// with no vendor name.
func (p *PCIIDs) Name(vendor, device string) string {
	vendor, device = strings.ToLower(vendor), strings.ToLower(device)
	vendorName, ok := p.vendors[vendor]
	if !ok {
		return ""
	}
	if deviceName, ok := p.devices[vendor+":"+device]; ok {
		return vendorName + " " + deviceName
	}
	return vendorName + " device " + device
}
