package main

// The device this driver publishes for one render node.
//
// The device is a statement about a GPU and its media driver. It
// delivers no device node, and any number of claims can allocate it at
// once. A workload's claim pairs it with the `liken.sh` render node of
// the same GPU through a matchAttribute constraint, so the claim
// receives the render node from `liken` and the guarantee from this
// device.

// pciBusIDAttribute is the attribute that pairs this driver's device
// with the render node. Kubernetes defines it as a standard attribute
// in the resource.kubernetes.io domain, which belongs to no single
// driver, and `liken` publishes it on every device of a PCI card. A
// bare attribute name belongs to the driver that published it, so
// `liken.sh/address` and `media.liken.sh/address` are two names and a
// constraint cannot match one against the other. The reference is
// https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/.
const pciBusIDAttribute = "resource.kubernetes.io/pciBusID"

// vaDriverAttribute names the media driver that stated the
// capabilities, such as "Intel iHD driver for Intel(R) Gen Graphics -
// 26.1.2 ()". A person reads it to see which driver a value came from.
const vaDriverAttribute = "vaDriver"

// identityAttributes are the attributes of the `liken` device that this
// device repeats, so `kubectl get resourceslice` shows which GPU each
// statement is about.
var identityAttributes = []string{"address", "driver", "vendor", "product", "name"}

// maxAttributeString is the API's limit on the length of a string
// attribute.
const maxAttributeString = 64

// mediaDevice builds the device for one GPU from the `liken` device of
// its render node and the report of its driver. A failed query passes
// an empty report, so every capability is false.
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
	// The PCI address and the standard attribute hold the same value,
	// so a `liken` release that predates the standard attribute still
	// gives this device its pairing value.
	if gpu.stringAttribute("bus") == "pci" {
		attrs[pciBusIDAttribute] = attrString(gpu.stringAttribute("address"))
	}
	if facts.Vendor != "" {
		attrs[vaDriverAttribute] = attrString(truncate(facts.Vendor, maxAttributeString))
	}
	shared := true
	return SliceDevice{Name: gpu.Name, Attributes: attrs, AllowMultipleAllocations: &shared}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
