package main

// A device name is built from the hardware's identity, in forms like
// the ones udev uses for /dev/snd/by-id and /dev/snd/by-path. Every
// form starts with the machine's name, because Sinks and Sources are
// cluster-scoped, and two machines that built the same name would
// write the same object. After the machine name come:
//
//   - Onboard card: "pci", the PCI address, and the PCM id. The PCI
//     address repeats on every machine of the same model.
//   - USB card with a serial: "usb", vendor, product, serial, and the
//     PCM id. The serial tells apart two dongles of one model on one
//     machine. It is not unique across machines, because dongles of one
//     model can all report the same serial.
//   - USB card with no serial: "usb", the port path, and the PCM id.
//
// The PCM id is the driver's name for the PCM device, such as "HDMI 0"
// or "USB Audio". The name does not use the PCM device number, because
// the kernel can assign a different number at each boot.
//
// When the name of a USB card with a serial is longer than
// maxDeviceName, it uses a hash of the serial in place of the serial
// (serialHash). A name that is still longer is refused.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// PairingAttribute pairs a screen with its speakers. display-operator
// publishes the same attribute, so one ResourceClaim can request a device
// from each driver with a matchAttribute constraint across the two. The
// scheduler qualifies an attribute written without a domain with the
// domain of the driver that publishes it, so a bare name from each driver
// would be two attributes that never match. The domain monitor.liken.sh
// belongs to neither driver. The scheduler compares the two values byte
// for byte, so monitorID here and in display-operator/names.go must
// return the same string for the same monitor.
const PairingAttribute = "monitor.liken.sh/id"

// maxDeviceName is the length limit of a DNS label, which a DRA device
// name must be. endpointName refuses a longer name instead of cutting
// it, because a person cannot predict a cut name from the hardware.
const maxDeviceName = 63

// serialHash returns the first eight hex digits of the SHA-256 of a USB
// serial, as sysfs reports it. endpointName uses it in place of the
// serial when the name with the serial is longer than maxDeviceName. A
// person can compute the hash from the serial, so the name stays
// predictable from the hardware. Eight digits make a collision between
// two dongles on one machine unlikely, and leave room for a machine name
// of 22 characters in a capture name whose PCM id is USB Audio.
//
// endpointName checks the length of each name alone. A playback name of
// 56 to 63 characters keeps the serial, and the capture name of the same
// dongle, which is 8 characters longer, uses the hash.
func serialHash(serial string) string {
	sum := sha256.Sum256([]byte(serial))
	return hex.EncodeToString(sum[:])[:8]
}

// captureSuffix keeps a capture endpoint's name apart from the playback
// endpoint of the same PCM device. A USB card serves both directions
// through one PCM device with one id, so both endpoints would derive
// the same name, and two devices in one pool cannot have the same name.
// Playback has no suffix, because claims request sinks more often.
const captureSuffix = "-capture"

// alsaAddress returns the card and PCM device numbers that the kernel
// assigned in this boot, such as card0-pcm3. The PipeWire node name is
// built from it and not from the device name, because nodeConfig
// declares a node for every PCM device, including one that endpointName
// refuses, and a layout change rewrites the drop-in from the card
// and PCM numbers alone. A number that changes at a reboot does no harm,
// because the declare init container writes the drop-in again at
// each pod start.
func alsaAddress(card, pcm int) string {
	return fmt.Sprintf("card%d-pcm%d", card, pcm)
}

// endpointName builds the DRA device name of one ALSA endpoint. A card
// that moves to another machine, or a USB card with no serial that moves
// to another port, gets a new name, as with udev's names. An error
// names the missing part of the identity, a bus that the operator
// cannot name a card by, or a name over maxDeviceName.
func endpointName(machine string, card cardIdentity, pcmID string, capture bool) (string, error) {
	pcm := slug(pcmID)
	if pcm == "" {
		return "", fmt.Errorf("the driver reports no id for the PCM device")
	}
	if card.Bus != pciBus && card.Bus != usbBus {
		return "", fmt.Errorf("the card is on no bus this operator can name it by")
	}
	node := slug(machine)
	if node == "" {
		return "", fmt.Errorf("this machine has no name to build a %s card's name from", card.Bus)
	}

	suffix := ""
	if capture {
		suffix = captureSuffix
	}

	var name string
	switch {
	case card.Bus == usbBus && card.Serial != "":
		vendor, product, serial := slug(card.Vendor), slug(card.Product), slug(card.Serial)
		if vendor == "" || product == "" {
			return "", fmt.Errorf("the USB card reports no vendor or product identifier")
		}
		name = strings.Join([]string{node, usbBus, vendor, product, serial, pcm}, "-") + suffix
		if len(name) > maxDeviceName {
			name = strings.Join([]string{node, usbBus, vendor, product, serialHash(card.Serial), pcm}, "-") + suffix
		}
	default:
		location := slug(card.Location)
		if location == "" {
			return "", fmt.Errorf("sysfs reports no %s address for the card", card.Bus)
		}
		name = strings.Join([]string{node, card.Bus, location, pcm}, "-") + suffix
	}

	if len(name) > maxDeviceName {
		return "", fmt.Errorf("the name %s is %d characters, and a device name has at most %d",
			name, len(name), maxDeviceName)
	}
	return name, nil
}

// slug turns one part of an identity into part of a DNS label:
// lowercase letters and digits, with one dash for each run of other
// characters and no dash at either end.
func slug(value string) string {
	var slugged strings.Builder
	separated := false
	for _, character := range strings.ToLower(value) {
		alphanumeric := (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9')
		if !alphanumeric {
			separated = true
			continue
		}
		if separated && slugged.Len() > 0 {
			slugged.WriteByte('-')
		}
		separated = false
		slugged.WriteRune(character)
	}
	return slugged.String()
}

// nameEndpoints sets DeviceName on each endpoint it can name, and
// returns a reason for each one it cannot. There is no fallback name,
// because a name built from the card number would point at different
// hardware when the cards register in a different order.
func nameEndpoints(machine string, outputs []alsaEndpoint) (named []alsaEndpoint, refused []string) {
	for _, output := range outputs {
		name, err := endpointName(machine, output.Identity, output.PCMID, output.Capture)
		if err != nil {
			refused = append(refused, fmt.Sprintf("%s: %v", output.Address(), err))
			continue
		}
		output.DeviceName = name
		named = append(named, output)
	}
	return named, refused
}

// endpointInventory maps each device name to the endpoint that the
// last reconcile pass published under it. A prepare call carries only
// the device name, which does not contain the card and PCM numbers, so
// the plugin looks the name up here. The reconcile loop writes the map
// and the kubelet's gRPC handlers read it.
type endpointInventory struct {
	mutex     sync.RWMutex
	endpoints map[string]alsaEndpoint
}

// publish replaces the inventory with the endpoints of one reconcile
// pass. On a nil inventory it does nothing, so a caller needs no nil
// check.
func (i *endpointInventory) publish(outputs []alsaEndpoint) {
	if i == nil {
		return
	}
	endpoints := make(map[string]alsaEndpoint, len(outputs))
	for _, output := range outputs {
		endpoints[output.Name()] = output
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.endpoints = endpoints
}

// sinkNames returns the device names of the playback endpoints, sorted.
func (i *endpointInventory) sinkNames() []string {
	if i == nil {
		return nil
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	var names []string
	for _, name := range slices.Sorted(maps.Keys(i.endpoints)) {
		if !i.endpoints[name].Capture {
			names = append(names, name)
		}
	}
	return names
}

// lookup returns the endpoint published under a device name. The name
// of a Bluetooth speaker returns false, because the inventory holds only
// ALSA endpoints.
func (i *endpointInventory) lookup(name string) (alsaEndpoint, bool) {
	if i == nil {
		return alsaEndpoint{}, false
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	output, published := i.endpoints[name]
	return output, published
}

// speakerName builds a Bluetooth speaker's device name from its MAC
// address, because the address is the identity that BlueZ reports that
// stays the same across a reboot. bluetooth-operator names a
// Peripheral in the same dashed lowercase form.
func speakerName(address string) string {
	return strings.ReplaceAll(normalizeMAC(address), ":", "-")
}

// speakerFromDeviceName returns the MAC address in a name that
// speakerName built, so a prepare call finds the speaker from the
// device name alone. It returns false for any other name, such as the
// name of an ALSA endpoint.
func speakerFromDeviceName(name string) (string, bool) {
	address := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", ":")
	if !validMAC(address) {
		return "", false
	}
	return address, true
}

// normalizeMAC returns the key of a speaker: the address in lowercase,
// with colons. BlueZ over D-Bus and api.bluez5.address in pw-dump both
// report the uppercase form, AA:BB:CC:DD:EE:FF. The key is lowercase
// because a device name must be lowercase, and speakerFromDeviceName
// returns the key from the device name.
func normalizeMAC(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// publishedMAC returns the uppercase colon form of a MAC address, as
// bluetoothctl and the label on a speaker print it.
func publishedMAC(address string) string {
	return strings.ToUpper(normalizeMAC(address))
}

// validMAC reports whether address is six colon-separated pairs of hex
// digits, in either case.
func validMAC(address string) bool {
	octets := strings.Split(normalizeMAC(address), ":")
	if len(octets) != 6 {
		return false
	}
	for _, octet := range octets {
		if len(octet) != 2 {
			return false
		}
		for _, c := range octet {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// monitorID builds the value of PairingAttribute from the facts that
// both an ELD block and an EDID block contain: the manufacturer, the
// product code, and the monitor name, when there is one:
//
//	GSM 0x5b09 "LG ULTRAWIDE"    -> gsm-5b09-lg-ultrawide
//	BOE 0x095f ""                -> boe-095f
//
// This operator reads the ELD and display-operator reads the EDID, and
// for a panel with no name descriptor, the name can be empty in one of
// the two. So an empty name leaves the value without a name. If it made
// the value empty, only one operator would publish the attribute, and a
// claim that matches on it would stay Pending with no reason reported.
//
// EDID pads a name descriptor with spaces, and a graphics driver can
// strip the padding, so the name is trimmed before its spaces become
// dashes.
//
// An empty manufacturer returns an empty value, and the caller
// publishes no attribute, so a claim that matches on it allocates
// nothing for a monitor that this operator cannot identify.
func monitorID(manufacturer string, product uint16, name string) string {
	manufacturer = strings.ToLower(strings.TrimSpace(manufacturer))
	if manufacturer == "" {
		return ""
	}
	id := manufacturer + "-" + hexProduct(product)
	if dashedName := dashed(name); dashedName != "" {
		id += "-" + dashedName
	}
	return id
}

// hexProduct returns a product code as four lowercase hex digits, the
// form in monitorID and in the product attribute.
func hexProduct(product uint16) string {
	return fmt.Sprintf("%04x", product)
}

// dashed returns the same string as slug in display-operator/names.go,
// because the pairing attribute must match byte for byte. This
// package's slug also replaces punctuation with a dash, so a monitor
// name with punctuation would give a different value.
func dashed(name string) string {
	fields := strings.Fields(strings.ToLower(name))
	return strings.Join(fields, "-")
}

// pnpID decodes the three-letter manufacturer id that EDID packs into
// two bytes, five bits per letter, first letter in the high bits. It
// reads the bytes in EDID order, big-endian: 0x1e6d is GSM, which is
// LG. The kernel prints an ELD's manufacture_id with the two bytes
// swapped, so the ELD parser reads the raw bytes in EDID order before
// it calls pnpID, and both operators decode a manufacturer the same way.
func pnpID(id uint16) string {
	letters := [3]byte{
		byte((id>>10)&0x1f) + 'A' - 1,
		byte((id>>5)&0x1f) + 'A' - 1,
		byte(id&0x1f) + 'A' - 1,
	}
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return ""
		}
	}
	return string(letters[:])
}
