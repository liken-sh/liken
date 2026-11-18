package main

// Each device that display-operator publishes is named after the DRM
// connector of one graphics card output, such as HDMI-A-1 or DP-2. The
// connector belongs to the card, so the name stays the same across
// reboots and whether or not a monitor is plugged in. The monitor's
// EDID facts are published as attributes of the device.
//
// A DRA device name must be a DNS label, so the device name is the
// connector in lowercase (hdmi-a-1). The connector attribute keeps the
// kernel's spelling, because weston.ini and kernel log lines use it.
//
// The attribute monitor.liken.sh/id identifies a monitor, and
// audio-operator publishes the same value for that monitor's speakers.
// So one claim can request a screen from this driver and speakers from
// audio-operator, and a matchAttribute constraint on the attribute
// selects a screen and speakers of the same monitor. The attribute
// needs a domain, because an attribute with no domain belongs to the
// driver that publishes it, and a bare name from each driver would
// never match.

import (
	"fmt"
	"strings"
)

// pairingAttribute is the attribute that display-operator and
// audio-operator both publish.
const pairingAttribute = "monitor.liken.sh/id"

// deviceName returns the connector in lowercase. Kernel connector names
// are otherwise valid DNS labels already.
func deviceName(connector string) string {
	return strings.ToLower(strings.TrimSpace(connector))
}

// A connector whose panel supports DDC/CI also publishes a control
// device, named with this suffix. An allocation result has only a
// device name, so prepare reads the suffix to tell which of the
// connector's devices a claim holds.
const controlSuffix = "-control"

// controlName returns the name of the control device for a connector.
func controlName(connector string) string {
	return deviceName(connector) + controlSuffix
}

// outputOfControl returns the output device name for a control device
// name, and false for any other name. Kernel connector names are a type
// and an index, so no output device name ends in -control.
func outputOfControl(device string) (string, bool) {
	return strings.CutSuffix(device, controlSuffix)
}

// Every connector also publishes a draw device, named with this
// suffix. Many claims can hold the draw device at once, and each gets
// its own Wayland socket. The output device allows one claim.
const drawSuffix = "-draw"

// drawName returns the name of the draw device for a connector.
func drawName(connector string) string {
	return deviceName(connector) + drawSuffix
}

// outputOfDraw returns the output device name for a draw device name,
// and false for any other name.
func outputOfDraw(device string) (string, bool) {
	return strings.CutSuffix(device, drawSuffix)
}

// appID returns the value of DISPLAY_APP_ID for a claim on one output,
// which is the output's device name (hdmi-a-1 for HDMI-A-1).
//
// Nothing routes by it. The operator finds the claim that drew a
// surface from the Wayland socket that the layout module reports the
// client connected on. The variable is still set because consumer
// images pass it to their toolkit.
func appID(connector string) string {
	return deviceName(connector)
}

// monitorID returns the value both operators publish as
// monitor.liken.sh/id: the manufacturer's PNP id in lowercase, the
// product code as four lowercase hexadecimal digits, and the monitor
// name descriptor in lowercase with each run of spaces replaced by one
// dash. An LG ultrawide gives gsm-5b09-lg-ultrawide.
//
// audio-operator builds the same value from the HDMI ELD, and the
// scheduler compares the two values byte for byte. A difference of one
// character means no claim that pairs them can be allocated, so both
// operators follow these rules exactly:
//
//   - When the manufacturer does not decode, return an empty string, so
//     no attribute is published. Otherwise the value would match every
//     other monitor whose manufacturer did not decode.
//   - Append the name only when it is not empty after trimming. A
//     monitor with no name gives boe-095f, with no trailing dash. The
//     ELD has only the manufacturer, product code, and monitor name, so
//     a monitor with no name still matches its speakers on the first
//     two parts.
func monitorID(edid EDID) string {
	if edid.Manufacturer == "" {
		return ""
	}
	id := fmt.Sprintf("%s-%04x", strings.ToLower(edid.Manufacturer), edid.ProductCode)
	if name := slug(edid.ModelName); name != "" {
		id += "-" + name
	}
	return id
}

// slug lowercases a monitor name and replaces each run of spaces with
// one dash, so "LG HDR WQHD" becomes lg-hdr-wqhd.
func slug(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), "-")
}
