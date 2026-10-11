package capture

// A Bare is one of the bare drivers in indi/standard/bare/: a driver
// for one INDI interface that turns on every capability, defines
// nothing of its own, and reports connected with no hardware. What it
// defines is libindi's standard set for that interface. Name is the
// name of its capture, and steps are the changes its capture sends
// after connect.
type Bare struct {
	Name   string
	Driver string
	steps  func(device string) []Phase
}

// DefaultDevice is the capture of the bare driver that hosts no
// interface: the properties of INDI::DefaultDevice and of the serial
// and TCP connection plugins. They belong to every device and to no
// interface, so every other capture leaves them out.
const DefaultDevice = "device"

// Bares are the bare drivers, one capture each. An interface whose
// capabilities cannot all be on at once has one capture for each
// variant: a telescope's park type sets the members of
// TELESCOPE_PARK_POSITION, a focuser defines FOCUS_TIMER only when it
// cannot move to an absolute position, and a dome is a rotating dome
// or a roll-off roof. Switch is OUTPUT and INPUT, one capture each.
var Bares = []Bare{
	{Name: DefaultDevice, Driver: "indi_bare_device", steps: tcp},
	{Name: "telescope-ra-dec", Driver: "indi_bare_telescope_ra_dec", steps: pierSide},
	{Name: "telescope-ha-dec", Driver: "indi_bare_telescope_ha_dec", steps: pierSide},
	{Name: "telescope-az-alt", Driver: "indi_bare_telescope_az_alt", steps: pierSide},
	{Name: "telescope-ra-dec-encoder", Driver: "indi_bare_telescope_ra_dec_encoder", steps: pierSide},
	{Name: "telescope-az-alt-encoder", Driver: "indi_bare_telescope_az_alt_encoder", steps: pierSide},
	{Name: "telescope-simple", Driver: "indi_bare_telescope_simple", steps: pierSide},
	{Name: "ccd", Driver: "indi_bare_ccd"},
	{Name: "filter", Driver: "indi_bare_filter"},
	{Name: "focuser", Driver: "indi_bare_focuser"},
	{Name: "focuser-timer", Driver: "indi_bare_focuser_timer"},
	{Name: "rotator", Driver: "indi_bare_rotator"},
	{Name: "dustcap", Driver: "indi_bare_dustcap"},
	{Name: "lightbox", Driver: "indi_bare_lightbox"},
	{Name: "pac", Driver: "indi_bare_pac"},
	{Name: "gps", Driver: "indi_bare_gps"},
	{Name: "dome", Driver: "indi_bare_dome"},
	{Name: "dome-roof", Driver: "indi_bare_dome_roof"},
	{Name: "weather", Driver: "indi_bare_weather"},
	{Name: "output", Driver: "indi_bare_output"},
	{Name: "input", Driver: "indi_bare_input"},
	{Name: "receiver", Driver: "indi_bare_receiver"},
}

// Script is what a capture sends to the bare driver: the baseline, the
// connect, and the driver's steps.
func (b Bare) Script(device string) Script {
	phases := []Phase{
		{"baseline", Baseline},
		{"connect", switchMessage(device, "CONNECTION", "CONNECT")},
	}
	if b.steps != nil {
		phases = append(phases, b.steps(device)...)
	}
	return Script{Phases: phases}
}

// tcp selects the TCP connection plugin. Each plugin defines its
// properties only while it is the selected one, and the serial plugin
// is selected first.
func tcp(device string) []Phase {
	return []Phase{{"tcp", switchMessage(device, "CONNECTION_MODE", "CONNECTION_TCP")}}
}

// pierSide turns on the telescope's simulated pier side. A telescope
// that simulates its pier side defines TELESCOPE_PIER_SIDE at connect
// and deletes it at once while the simulation is off, and defines it
// again when the simulation turns on.
func pierSide(device string) []Phase {
	return []Phase{{"pier-side", switchMessage(device, "SIMULATE_PIER_SIDE", "SIMULATE_YES")}}
}
