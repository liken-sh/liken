package schema

// A unit is the measurement unit of a numeric field. The generator
// fails when a hand-written description of the field does not contain
// word. sentence ends a generated description.
type unit struct {
	word, sentence string
}

// units maps the short name that a fields file uses to its unit. The
// generator fails on a short name that is not in this map, so a typo
// cannot reach a description.
var units = map[string]unit{
	"s":        {"seconds", "The unit is seconds."},
	"ms":       {"milliseconds", "The unit is milliseconds."},
	"h":        {"hours", "The unit is hours."},
	"deg":      {"degrees", "The unit is degrees."},
	"arcsec/s": {"arcseconds per second", "The unit is arcseconds per second."},
	"degC":     {"degrees Celsius", "The unit is degrees Celsius."},
	"degC/min": {"degrees Celsius per minute", "The unit is degrees Celsius per minute."},
	"px":       {"pixels", "The unit is pixels."},
	"um":       {"micrometers", "The unit is micrometers."},
	"mm":       {"millimeters", "The unit is millimeters."},
	"m":        {"meters", "The unit is meters."},
	"percent":  {"percent", "The unit is percent."},
	"steps":    {"steps", "The unit is the motor's steps."},
	"rpm":      {"revolutions per minute", "The unit is revolutions per minute."},
	"Hz":       {"hertz", "The unit is hertz."},
	"MB":       {"megabytes", "The unit is megabytes."},
	"fps":      {"frames per second", "The unit is frames per second."},
	"bits":     {"bits", "The unit is bits."},
	"frames":   {"frames", "The unit is frames."},
	"count":    {"", "It is a count, with no unit."},
	"factor":   {"", "It is a factor, with no unit."},
	"driver":   {"the driver's own units", "The unit is the driver's own."},
	"label":    {"label names", "The unit is the one that the element's label names."},
}
