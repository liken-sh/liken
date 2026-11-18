package observatory

// The conditions of a device's state, which procedure triggers read.
// The operator sets them only while the device is connected. Parked and
// Open are Unknown while their property is Busy, because the device is
// moving. A condition is also Unknown when its property has neither of
// its switches On.
const (
	// Parked is True while a Dome's DOME_PARK or a Mount's
	// TELESCOPE_PARK has PARK On and is not Busy. It is False while
	// UNPARK is On.
	ConditionParked = "Parked"
	// Open is True while a Dome's shutter or a DustCap's cover is open
	// and not moving: SHUTTER_OPEN On in DOME_SHUTTER, or UNPARK On in
	// CAP_PARK.
	ConditionOpen = "Open"
	// Lit is True while a FlatPanel's FLAT_LIGHT_CONTROL has
	// FLAT_LIGHT_ON On, and False while FLAT_LIGHT_OFF is On.
	ConditionLit = "Lit"
	// Cooling is True while a Camera's CCD_COOLER has COOLER_ON On, and
	// False while COOLER_OFF is On.
	ConditionCooling = "Cooling"
	// Safe is True while a WeatherStation reports Safe, and False, with
	// the reason Warning or Danger, when it reports that level.
	ConditionSafe = "Safe"
)
