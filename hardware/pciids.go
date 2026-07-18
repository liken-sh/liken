package hardware

// pci.ids: the naming database for PCI hardware.
//
// USB devices carry their own names — manufacturer and product
// strings the kernel reads at enumeration — but PCI devices carry
// only numbers, and the names lspci prints come from a community
// database, pci.ids, maintained by the hwdata project. liken
// vendors it as a pinned flat file (the hwdata domain owns the pin)
// so an unclaimed-device report can say "Red Hat, Inc. Virtio 1.0
// GPU" instead of "1af4:1050". The dependency is soft by design:
// every caller falls back to the numeric IDs when the file is
// missing, because naming is a courtesy and reporting is the job.

import (
	"os"
	"strings"
)

// PCIIDs is the loaded database: vendors by their 4-digit hex ID,
// devices by vendor + device. The file's third level (subsystem
// names) and its trailing class-code section are deliberately not
// parsed; the report names devices, and class words come from the
// spec-defined table in names.go.
type PCIIDs struct {
	vendors map[string]string
	devices map[string]string
}

// LoadPCIIDs parses the pci.ids format: vendor lines flush left
// ("1af4  Red Hat, Inc."), device lines one tab in, subsystem lines
// two tabs in, and single-letter sections ("C 03  Display
// controller") after the vendors.
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
			// Subsystem names: more precision than a status line needs.
		case strings.HasPrefix(line, "\t"):
			if id, name, found := strings.Cut(line[1:], "  "); found && vendor != "" {
				ids.devices[vendor+":"+id] = name
			}
		default:
			id, name, found := strings.Cut(line, "  ")
			// A non-hex first field (the "C 03" class sections) ends
			// the vendor list for our purposes.
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

// Name renders a vendor:device pair in words: both names when the
// database knows the device, the vendor plus the numeric device ID
// when it only knows the vendor, and nothing at all for an unknown
// vendor — the caller's numeric fallback reads better than a bare
// hex device ID with no maker.
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
