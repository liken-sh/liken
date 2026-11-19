package main

import "unicode/utf8"

// This driver publishes one device for each render node. The device
// describes what the GPU's media driver can decode, encode, and scale.
// It has no device node, and any number of claims can allocate it at
// once. A workload's claim pairs it with the `liken.sh` render node of
// the same GPU through a matchAttribute constraint, so the pod gets the
// render node from `liken` and the capabilities from this device.

// pciBusIDAttribute is the attribute that the claim's matchAttribute
// constraint compares. The constraint needs an attribute in a domain
// that both drivers publish, because an attribute name with no domain
// belongs to its driver: `liken.sh/address` and `media.liken.sh/address`
// are different names. Kubernetes defines this standard attribute at
// https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/
// and `liken` publishes it on every device of a PCI card.
const pciBusIDAttribute = "resource.kubernetes.io/pciBusID"

// vaDriverAttribute is the vendor string of the VA-API driver that
// reported the capabilities, such as "Intel iHD driver for Intel(R) Gen
// Graphics - 26.1.2 ()".
const vaDriverAttribute = "vaDriver"

// identityAttributes are copied from the `liken` device, so a person
// who reads the ResourceSlice can tell which GPU a media device describes.
var identityAttributes = []string{"address", "driver", "vendor", "product", "name"}

// maxAttributeString is the longest string value that the API accepts
// in a device attribute, counted in bytes.
const maxAttributeString = 64

// mediaDevice builds the media device for one GPU from the `liken`
// device of its render node and the driver's report. When the query
// failed, the caller passes an empty report, and every capability is
// false.
func mediaDevice(gpu SliceDevice, facts report) SliceDevice {
	attrs := map[string]DeviceAttribute{}
	for name, value := range capabilitiesOf(facts) {
		attrs[name] = attrBool(value)
	}
	for _, name := range identityAttributes {
		if value := gpu.stringAttribute(name); value != "" {
			attrs[name] = attrString(value)
		}
	}
	// The value is the PCI address, so this device has the attribute
	// even when an older `liken` release published the render node's
	// device without it.
	if gpu.stringAttribute("bus") == "pci" {
		attrs[pciBusIDAttribute] = attrString(gpu.stringAttribute("address"))
	}
	if facts.Vendor != "" {
		attrs[vaDriverAttribute] = attrString(truncate(facts.Vendor, maxAttributeString))
	}
	shared := true
	return SliceDevice{Name: gpu.Name, Attributes: attrs, AllowMultipleAllocations: &shared}
}

// truncate cuts s to at most limit bytes, at a character boundary. The
// API server counts the bytes of an attribute, and encoding/json writes a
// byte that is not UTF-8 as the three bytes of U+FFFD, so a cut through a
// character would make the value longer than the limit.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
