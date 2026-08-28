package main

// The identity of a published device.
//
// A device name is built from the hardware's own identity, in the
// same three forms udev encodes into /dev/snd/by-id and by-path. An
// onboard card is named by the machine and its PCI address, because
// a PCI address repeats on every machine of the same model and the
// resources are cluster-scoped. A USB card with a serial is named by
// its vendor, product, and serial and by no machine at all, because
// the dongle keeps its identity when it moves and status.node says
// where it is. A USB card with no serial is named by the machine and
// its port. Each form ends in the driver's own name for the PCM
// device, hdmi-0 or usb-audio, and not its device number: the id is
// stable per codec, and the number is this boot's.
//
// A name is a DNS label, so it holds 63 characters, and a name past
// that is refused rather than shortened. The ALSA address, card0-pcm3,
// survives only as the PipeWire node name.
//
// The pairing identity is a different thing. monitor.liken.sh/id
// names the monitor a claim asks for, in a domain that neither this
// driver nor the display operator owns, so that one ResourceClaim can
// hold a request against each driver and a matchAttribute constraint
// across the two. The scheduler compares a fully qualified attribute
// name across devices without regard to which driver published them.
// An attribute written without a domain belongs to the publishing
// driver's domain, so a bare monitorName from two drivers is two
// different names that never match.

import (
	"fmt"
	"strings"
	"sync"
)

// PairingAttribute is the fully qualified attribute that pairs a
// screen with its speakers. The display operator publishes the same
// name, and the two values must be identical byte for byte, so the
// derivation below is the contract between the two repositories.
const PairingAttribute = "monitor.liken.sh/id"

// maxDeviceName is the length of a DNS label, which is what a DRA
// device name must be. A name past it is refused rather than
// shortened, because a silently shortened name is a name nobody can
// predict from the hardware.
const maxDeviceName = 63

// captureSuffix separates a capture endpoint's name from the
// playback endpoint beside it. A USB card serves both directions
// through one PCM device with one id, so the two endpoints derive the
// same name, and two devices of one pool cannot share a name.
// Playback carries no word, because a sink is what a claim asks for
// most and its name reads cleaner without one.
const captureSuffix = "-capture"

// alsaAddress spells one PCM device's ALSA address, which is the
// card number and the device number the kernel assigned this boot.
// It is not a device name. It names the PipeWire node the operator
// declares, because the node is declared before the identity is read
// and lives only as long as the graph.
func alsaAddress(card, pcm int) string {
	return fmt.Sprintf("card%d-pcm%d", card, pcm)
}

// endpointName builds the DRA device name for one ALSA endpoint,
// from the hardware's own identity, in the three forms the file
// header describes. The serial form makes one trade: a dongle with a
// serial keeps its name when it moves, and a dongle without one
// becomes a new object at a new port, which is the trade udev made.
// An error names what was missing, and the caller refuses to publish
// the endpoint.
func endpointName(machine string, card cardIdentity, pcmID string, capture bool) (string, error) {
	pcm := slug(pcmID)
	if pcm == "" {
		return "", fmt.Errorf("the driver states no id for the PCM device")
	}

	var name string
	switch {
	case card.Bus == usbBus && card.Serial != "":
		vendor, product, serial := slug(card.Vendor), slug(card.Product), slug(card.Serial)
		if vendor == "" || product == "" {
			return "", fmt.Errorf("the USB card states no vendor or product identifier")
		}
		name = strings.Join([]string{usbBus, vendor, product, serial, pcm}, "-")
	case card.Bus == pciBus, card.Bus == usbBus:
		node, location := slug(machine), slug(card.Location)
		if node == "" {
			return "", fmt.Errorf("this machine has no name to build a %s card's name from", card.Bus)
		}
		if location == "" {
			return "", fmt.Errorf("sysfs states no %s address for the card", card.Bus)
		}
		name = strings.Join([]string{node, card.Bus, location, pcm}, "-")
	default:
		return "", fmt.Errorf("the card is on no bus this operator can name it by")
	}
	if capture {
		name += captureSuffix
	}

	if len(name) > maxDeviceName {
		return "", fmt.Errorf("the name %s is %d characters, and a device name holds %d",
			name, len(name), maxDeviceName)
	}
	return name, nil
}

// slug turns one part of an identity into a piece of a DNS label:
// lowercase letters, digits, and one dash for each run of anything
// else, with no dash on either end.
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

// nameEndpoints stamps each endpoint with the name it publishes
// under, and holds back the ones this operator cannot name. An
// endpoint with no name reaches no slice and no claim, because a
// name built from anything but the hardware's identity would follow
// the wrong hardware the first time a card registers in a different
// order.
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

// endpointInventory is what the last reconcile pass published, keyed
// by device name.
//
// A prepare call carries the allocated device's name and nothing
// else, and the name no longer holds the card and PCM numbers, so
// the plugin resolves a name against the inventory the reconcile
// pass read from the hardware. The reconcile loop writes it and the
// kubelet's gRPC handlers read it, which is why it holds a lock.
type endpointInventory struct {
	mutex     sync.RWMutex
	endpoints map[string]alsaEndpoint
}

// publish replaces the whole inventory with what one pass read. A
// nil inventory takes the write and drops it, so a caller that has
// none needs no branch.
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

// lookup resolves one device name to the endpoint it names. A name
// this operator did not publish resolves to nothing, and the prepare
// call fails with that name in its message.
func (i *endpointInventory) lookup(name string) (alsaEndpoint, bool) {
	if i == nil {
		return alsaEndpoint{}, false
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	output, published := i.endpoints[name]
	return output, published
}

// speakerName builds a speaker's device name from its peer MAC, the
// one identity BlueZ carries that survives a reboot, in the same
// dashed lowercase form the Bluetooth operator names a controller
// with.
func speakerName(address string) string {
	return strings.ReplaceAll(normalizeMAC(address), ":", "-")
}

// speakerFromDeviceName inverts speakerName. A prepare call carries
// the allocated device's name and nothing else.
func speakerFromDeviceName(name string) (string, bool) {
	address := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", ":")
	if !validMAC(address) {
		return "", false
	}
	return address, true
}

// normalizeMAC is the one form this operator keys a speaker on:
// lowercase with colons. BlueZ prints AA:BB:CC:DD:EE:FF over D-Bus
// and pw-dump prints the same string on the node, so one lowering
// makes the two sources agree.
func normalizeMAC(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// publishedMAC is the uppercase colon form. The label on a speaker
// shows it and bluetoothctl prints it, so it is the form a person
// already has the address written down in.
func publishedMAC(address string) string {
	return strings.ToUpper(normalizeMAC(address))
}

// validMAC accepts six colon-separated pairs of hexadecimal digits
// and nothing else, so an ALSA output's name never decodes as a
// speaker.
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

// monitorID builds the pairing value from the facts that an ELD block
// and an EDID block both hold: the manufacturer, the product code,
// and the monitor name.
//
// The manufacturer and the product code are the whole of the value,
// and the name is appended only when there is one:
//
//	GSM 0x5b09 "LG ULTRAWIDE"    -> gsm-5b09-lg-ultrawide
//	BOE 0x095f ""                -> boe-095f
//
// The name is optional because the two operators must agree on every
// monitor, and one of them can read a name the other cannot. A panel
// that publishes no name descriptor gives the display operator an
// empty string, so a rule that dropped the whole value for a missing
// name would make one operator publish a pairing attribute and the
// other publish none. A claim with a matchAttribute constraint across
// the two would then park forever, with nothing in the cluster saying
// why.
//
// The name is trimmed before the spaces inside it become dashes. EDID
// pads a descriptor with spaces to fill it, so an untrimmed name
// gives a value with a dash on the end, and a monitor whose padding
// the graphics driver already stripped would not match it.
//
// An empty return means the manufacturer did not decode, and the
// caller publishes no pairing attribute at all. A constraint on a
// missing attribute allocates nothing, which is the correct answer
// for a screen this operator cannot identify.
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

// hexProduct formats a monitor's product code the way the pairing
// identity and the product attribute spell it: four lowercase
// hexadecimal digits.
func hexProduct(product uint16) string {
	return fmt.Sprintf("%04x", product)
}

// dashed lowercases a monitor name and replaces each run of spaces
// with one dash.
func dashed(name string) string {
	fields := strings.Fields(strings.ToLower(name))
	return strings.Join(fields, "-")
}

// pnpID decodes the three-letter manufacturer identifier that EDID
// packs into two bytes, five bits for each letter, most significant
// letter first. The value here is the EDID's own big-endian form:
// 0x1e6d is GSM, which is LG.
//
// The kernel prints manufacture_id from an ELD block in the other
// byte order, because it reads the two bytes as a little-endian
// integer. The caller passes the bytes in EDID order, so this
// function and the display operator's own decoding agree.
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
