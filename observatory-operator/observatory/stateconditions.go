package observatory

// The conditions of a device's state, which the triggers of procedures
// read. A device reports them only while it is connected, so a trigger
// never acts on a state that the operator cannot read. A condition is
// Unknown while its device moves, and while the driver reports neither
// side of the switch.
const (
	// Parked is True while a Dome's DOME_PARK or a Mount's
	// TELESCOPE_PARK has PARK On and is not Busy.
	ConditionParked = "Parked"
	// Open is True while a Dome's shutter or a DustCap's cover is open
	// and does not move: SHUTTER_OPEN On in DOME_SHUTTER, or UNPARK On
	// in CAP_PARK.
	ConditionOpen = "Open"
	// Lit is True while a FlatPanel's FLAT_LIGHT_CONTROL has
	// FLAT_LIGHT_ON On.
	ConditionLit = "Lit"
	// Cooling is True while a Camera's CCD_COOLER has COOLER_ON On.
	ConditionCooling = "Cooling"
	// Safe is True while a WeatherStation reports Safe, and False with
	// the reason Warning or Danger.
	ConditionSafe = "Safe"
)
