package schema

// A unit is the unit of a number: the word that the field's
// description must hold, so the curated text names the same unit as
// its entry, and the sentence that a generated description ends with.
type unit struct {
	word, sentence string
}

// units holds each unit by the short name that a fields file gives.
// The list is closed, so a misspelled unit fails the generator, and
// each unit has one word, so every description names it the same way.
// A count and a factor have no word.
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
