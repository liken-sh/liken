package capture

// A Bare is one of the bare drivers in indi/standard/bare/. A bare
// driver implements one INDI interface with every capability turned on,
// adds no properties of its own, and connects with no hardware, so its
// capture holds libindi's standard properties for that interface. steps
// returns the changes that the capture sends after the driver connects.
type Bare struct {
	Name   string
	Driver string
	steps  func(device string) []Phase
}

// DefaultDevice is the capture of the bare driver that implements no
// interface: the properties of INDI::DefaultDevice and of the serial
// and TCP connection plugins. Every device defines them, so every
// other capture leaves them out.
const DefaultDevice = "device"

// Bares lists the bare drivers, with one capture for each. An interface
// whose capabilities cannot all be on at once has one driver and capture
// for each variant: a telescope's park type sets the members of
// TELESCOPE_PARK_POSITION, a focuser defines FOCUS_TIMER only when it
// cannot move to an absolute position, and a dome is either a rotating
// dome or a roll-off roof. A switch has one driver for OUTPUT and one
// for INPUT.
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

// Script returns what a capture sends to the bare driver: the baseline,
// the connect, and then the driver's own steps.
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

// tcp selects the TCP connection plugin, so that the capture records
// its properties. Each plugin defines its properties only while it is
// selected, and the serial plugin is selected by default.
func tcp(device string) []Phase {
	return []Phase{{"tcp", switchMessage(device, "CONNECTION_MODE", "CONNECTION_TCP")}}
}

// pierSide turns on the telescope's pier side simulation. While the
// simulation is off, the telescope defines TELESCOPE_PIER_SIDE at
// connect and deletes it at once, so the capture would not record it.
func pierSide(device string) []Phase {
	return []Phase{{"pier-side", switchMessage(device, "SIMULATE_PIER_SIDE", "SIMULATE_YES")}}
}
