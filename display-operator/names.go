package main

// The identity of a published device.
//
// The name is the connector the kernel assigns to one of the graphics
// card's outputs: HDMI-A-1, DP-2, eDP-1. A connector is a property of
// the card's own hardware, so the name is the same after a reboot, and
// it is the same whether a monitor is plugged into it or not. It says
// nothing about which monitor that is, which is why the EDID facts
// publish beside it.
//
// A DRA device name must be a DNS label, so the published name is the
// connector in lowercase: hdmi-a-1. The connector publishes as an
// attribute in the kernel's own spelling, because that is what
// weston.ini names, what a kernel log line prints, and what a person
// reads off `kubectl get resourceslice`.
//
// The pairing identity is a third name. `monitor.liken.sh/id` is a
// fully qualified attribute in a domain that neither this driver nor
// the audio operator owns, and both build it the same way, so one
// claim can hold a request against each driver with a matchAttribute
// constraint across the two. An attribute written without a domain
// belongs to the driver that published it, so a bare `model` here and
// a bare `model` there would never match.

import (
	"fmt"
	"strings"
)

// pairingAttribute is the fully qualified name that the display
// operator and the audio operator both publish. The domain is
// monitor.liken.sh, which is neither driver's own name: the
// QualifiedName documentation reserves an unqualified name for the
// publishing driver, so a shared value needs a domain that
// says what it identifies rather than who wrote it.
const pairingAttribute = "monitor.liken.sh/id"

// deviceName turns a connector into the DNS label that a DRA device
// name must be. The API rejects an uppercase letter, and every
// connector name the kernel produces is otherwise already a legal
// label: letters, digits, and dashes.
func deviceName(connector string) string {
	return strings.ToLower(strings.TrimSpace(connector))
}

// A connector whose panel answers DDC/CI publishes a second device,
// named after the output device with this suffix. The suffix carries
// the tie between the two, because an allocation result names a
// device and nothing else, so the name is all a prepare has to work
// out which of the connector's two devices the claim holds.
const controlSuffix = "-control"

// ControlName names the control device beside one output, built from
// the same connector, so the two names always agree.
func controlName(connector string) string {
	return deviceName(connector) + controlSuffix
}

// OutputOfControl answers which output a device name belongs to, and
// whether it named the control. The answer is unambiguous because the
// kernel names a connector after its physical type and its index, so
// no connector name ends in -control, and no output device's name can
// read as a control device's.
func outputOfControl(device string) (string, bool) {
	return strings.CutSuffix(device, controlSuffix)
}

// A connector also publishes a draw device, named after the output
// device with this suffix. The draw device shares the compositor
// socket with many claims at once, where the output device takes one
// claim. The suffix is the tie between the two, the same way
// controlSuffix ties the control device to its output.
const drawSuffix = "-draw"

// DrawName names the draw device beside one output, built from the
// same connector, so the two names always agree.
func drawName(connector string) string {
	return deviceName(connector) + drawSuffix
}

// OutputOfDraw answers which output a device name belongs to, and
// whether it named the draw companion. The answer is unambiguous for
// the reason outputOfControl gives: the kernel names a connector after
// its type and its index, so no connector name ends in -draw, and no
// output device's name can read as a draw device's.
func outputOfDraw(device string) (string, bool) {
	return strings.CutSuffix(device, drawSuffix)
}

// appID is the string the compositor routes a client's surface by.
// Version 0 uses the device name, and the operator writes it into
// weston.ini as the output's app-ids= line, so a claim on hdmi-a-1
// receives DISPLAY_APP_ID=hdmi-a-1 and a client that passes that to
// its toolkit puts its surface on that monitor.
//
// The app-id only routes a surface to an output; it grants nothing.
// The claim is what grants the output. The compositor
// refuses nothing: two clients that present one app-id both get the
// screen, one on top of the other. What stops that here is that the
// second pod cannot allocate an output the first pod holds.
func appID(connector string) string {
	return deviceName(connector)
}

// monitorID builds the value both operators publish under
// monitor.liken.sh/id: the manufacturer's PNP id in lowercase, the
// product code as four lowercase hexadecimal digits, and the monitor
// name descriptor in lowercase with each run of spaces replaced by one
// dash. An LG ultrawide reads gsm-5b09-lg-ultrawide.
//
// The rule is shared with the audio operator, which derives the same
// value from the HDMI ELD, and the scheduler compares the two byte for
// byte. Two drivers whose values differ by one character park every
// pairing claim forever, so the three steps are fixed:
//
//   - Decode the manufacturer. A decode that fails publishes no
//     pairing attribute at all, because a value with no manufacturer
//     in it would match every other monitor that also states none.
//   - Join the lowercase PNP id and the product code as four lowercase
//     hexadecimal digits.
//   - Append the dashed lowercase name only when the name says
//     something after trimming. A monitor with no name descriptor gets
//     the two-part form, boe-095f, and never a trailing dash or an
//     empty string. The ELD holds the manufacturer, the product
//     code, and the monitor name and nothing else, so a nameless panel
//     still pairs with its own speakers on the two parts both drivers
//     can build.
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

// slug turns a monitor name into the form the pairing identity
// uses: lowercase, with each run of spaces replaced by one dash.
// "LG HDR WQHD" becomes lg-hdr-wqhd.
func slug(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), "-")
}
