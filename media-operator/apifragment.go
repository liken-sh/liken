package main

// This file parses the query a capture route takes: the t= and xywh=
// dimensions of W3C Media Fragments 1.0, and the knobs width=,
// height=, framerate=, quality=, and bitrate=. The specification
// tells a user agent to ignore an invalid, unknown, or non-existent
// dimension (sections 6.2, 6.2.1, and 6.3.1). This API answers 400
// instead, because a query produces a new resource (sections 3.1 and
// 7.4), and a client that asked for a region must not silently get
// the whole screen.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// captureQuery is one parsed capture query. The times are numbers,
// because the composed route adds the lead-in to them and compares
// begin with end. The knobs stay the strings the caller wrote, because
// this API only checks their form and passes them to the sibling that
// gives them meaning.
type captureQuery struct {
	HasBegin  bool
	Begin     float64
	HasEnd    bool
	End       float64
	XYWH      string
	Width     string
	Height    string
	Framerate string
	Quality   string
	Bitrate   string
}

// captureBeginMax bounds t= begin at 60 s. A sidecar discards frames
// or samples until begin with a running pipeline, so every second of
// begin is a second of capture the sidecar runs for nobody, and the
// header timeout grows by begin as well.
const captureBeginMax = 60.0

// The query keys each aspect accepts. Each aspect names its own set,
// because a key travels to the sibling that gives it meaning: a
// bitrate= on the screen aspect would reach the display API, which
// refuses it, so this API refuses it first.
var (
	screenCaptureKeys = []string{"t", "xywh", "width", "height", "framerate", "quality"}
	audioCaptureKeys  = []string{"t", "bitrate"}
	mediaCaptureKeys  = []string{"t", "xywh", "width", "height", "framerate", "quality", "bitrate"}
)

// parseCaptureQuery reads the raw query the way Media Fragments
// section 5.1.1 says to: split on & and on the first = first, and
// percent-decode second. In that order t=10%2C20 is the interval
// 10,20, and t=%6ept:10 and t=npt%3a10 are both npt:10, as section
// 6.1.1 requires. net/url decodes first and splits second, so an
// encoded & or = inside a value would split there.
func parseCaptureQuery(rawQuery string, allowed []string) (captureQuery, error) {
	var query captureQuery
	var seen []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawName, rawValue, marked := strings.Cut(pair, "=")
		if !marked {
			return captureQuery{}, fmt.Errorf("the query part %q carries no value", pair)
		}
		name, err := url.PathUnescape(rawName)
		if err != nil {
			return captureQuery{}, fmt.Errorf("the query key %q cannot be decoded: %w", rawName, err)
		}
		value, err := url.PathUnescape(rawValue)
		if err != nil {
			return captureQuery{}, fmt.Errorf("the %s= value %q cannot be decoded: %w", name, rawValue, err)
		}
		if !slices.Contains(allowed, name) {
			return captureQuery{}, fmt.Errorf("the query key %q is not one of %s", name, strings.Join(allowed, ", "))
		}
		if slices.Contains(seen, name) {
			return captureQuery{}, fmt.Errorf("the query gives the key %q twice", name)
		}
		seen = append(seen, name)
		if err := query.setDimension(name, value); err != nil {
			return captureQuery{}, err
		}
	}
	// A capture takes width= or height= and scales the other side to
	// keep the frame's shape. Both together would name a shape the
	// frame does not have, so the pair is refused rather than passed
	// to the sidecar to pick one.
	if query.Width != "" && query.Height != "" {
		return captureQuery{}, fmt.Errorf("the query gives width=%s with height=%s, and a capture takes one of them", query.Width, query.Height)
	}
	return query, nil
}

// setDimension reads one key into the query. A key the allowed list
// admits but no case names is a refusal rather than a value the parser
// drops, so a key added to a list without a parser is a 400 a test
// sees and not a silent no-op.
func (q *captureQuery) setDimension(name, value string) error {
	switch name {
	case "t":
		return q.setTemporal(value)
	case "xywh":
		return q.setSpatial(value)
	case "width":
		return setCount(&q.Width, name, value)
	case "height":
		return setCount(&q.Height, name, value)
	case "framerate":
		return setCount(&q.Framerate, name, value)
	case "quality":
		return setCount(&q.Quality, name, value)
	case "bitrate":
		return setCount(&q.Bitrate, name, value)
	}
	return fmt.Errorf("the query key %q names no capture dimension", name)
}

// setTemporal reads t=, an optional npt: prefix and a begin, an end,
// or begin,end in normal play time. The interval is half-open per
// section 6.1.1: the begin is part of it and the end is the first
// instant that is not. An end at or before the begin names an empty
// interval, which would ask an upstream to capture nothing, so it is
// refused. A begin over captureBeginMax is refused too.
func (q *captureQuery) setTemporal(value string) error {
	interval := value
	if len(value) >= 4 && strings.EqualFold(value[:4], "npt:") {
		interval = value[4:]
	}
	beginText, endText, marked := strings.Cut(interval, ",")
	if beginText != "" {
		begin, err := parseNPT("t= begin", beginText)
		if err != nil {
			return err
		}
		if begin > captureBeginMax {
			return fmt.Errorf("the t= begin %s is over the %s second limit", formatNPT(begin), formatNPT(captureBeginMax))
		}
		q.HasBegin, q.Begin = true, begin
	}
	if marked && endText != "" {
		end, err := parseNPT("t= end", endText)
		if err != nil {
			return err
		}
		q.HasEnd, q.End = true, end
	}
	if !q.HasBegin && !q.HasEnd {
		return fmt.Errorf("the t= value %q names no time", value)
	}
	if q.HasBegin && q.HasEnd && q.Begin >= q.End {
		return fmt.Errorf("the t= end %s is not after the begin %s", formatNPT(q.End), formatNPT(q.Begin))
	}
	return nil
}

// parseNPT reads the three normal play time forms of section 4.2.1:
// ss, mm:ss, and hh:mm:ss, with an optional fraction on the seconds.
// It reads the fields from the seconds up, because the seconds field
// is always the last one and its rules change when a field stands
// beside it.
func parseNPT(label, text string) (float64, error) {
	refused := fmt.Errorf("the %s %q is not a time in ss, mm:ss, or hh:mm:ss form", label, text)
	fields := strings.Split(text, ":")
	if len(fields) > 3 {
		return 0, refused
	}
	seconds, ok := parseSeconds(fields[len(fields)-1], len(fields) > 1)
	if !ok {
		return 0, refused
	}
	if len(fields) == 1 {
		return seconds, nil
	}
	minutes, ok := parseWhole(fields[len(fields)-2], true)
	if !ok {
		return 0, refused
	}
	seconds += minutes * 60
	if len(fields) == 2 {
		return seconds, nil
	}
	hours, ok := parseWhole(fields[0], false)
	if !ok {
		return 0, refused
	}
	return seconds + hours*3600, nil
}

// parseSeconds reads the seconds field. It alone takes a fraction,
// because NPT puts the fraction on the seconds. A seconds field beside
// a minutes field must be two digits under sixty, per the grammar, so
// 1:75 is refused rather than read as 135 seconds.
func parseSeconds(text string, beside bool) (float64, bool) {
	whole, fraction, _ := strings.Cut(text, ".")
	if !digits(whole) || !digitsOrEmpty(fraction) {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(text, "."), 64)
	if err != nil {
		return 0, false
	}
	if beside && (len(whole) != 2 || seconds >= 60) {
		return 0, false
	}
	return seconds, true
}

// parseWhole reads a minutes or hours field. The minutes field is two
// digits under sixty, per the grammar. The hours field is any number
// of digits, because the grammar bounds it nowhere.
func parseWhole(text string, bounded bool) (float64, bool) {
	if !digits(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	if bounded && (len(text) != 2 || value >= 60) {
		return 0, false
	}
	return value, true
}

// setSpatial checks xywh=, four whole numbers under an optional
// pixel: or percent: prefix, and keeps the value as it was written.
// This API does not know the frame's size, so it passes the region
// through, and the display sidecar that owns the frame clips a region
// the frame does not hold, per section 6.1.2.
func (q *captureQuery) setSpatial(value string) error {
	refused := fmt.Errorf("the xywh= value %q is not four whole numbers under an optional pixel: or percent: prefix", value)
	region := value
	if rest, found := strings.CutPrefix(value, "pixel:"); found {
		region = rest
	}
	if rest, found := strings.CutPrefix(value, "percent:"); found {
		region = rest
	}
	fields := strings.Split(region, ",")
	if len(fields) != 4 {
		return refused
	}
	for _, field := range fields {
		if !digits(field) {
			return refused
		}
	}
	q.XYWH = value
	return nil
}

// setCount checks a knob: a positive whole number, since a zero width
// or a zero bitrate names no capture. It stays a string, because this
// API passes it to the sibling that gives it meaning and never does
// arithmetic on it.
func setCount(target *string, name, value string) error {
	if !digits(value) || strings.TrimLeft(value, "0") == "" {
		return fmt.Errorf("the %s= value %q is not a positive whole number", name, value)
	}
	*target = value
	return nil
}

// digits checks the characters itself rather than letting strconv
// decide, because strconv accepts a sign, an exponent, an infinity,
// and a hexadecimal form, and none of those is a time or a size a
// sibling should be asked for.
func digits(text string) bool {
	return text != "" && digitsOrEmpty(text)
}

func digitsOrEmpty(text string) bool {
	for _, character := range text {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// shiftedTemporal is the caller's interval with the lead-in added to
// each end, so the composed route asks each upstream for
// t=begin+L,end+L and both sidecars have a running pipeline before
// their zero. An absent begin is zero, which the lead-in moves the same
// way it moves a begin the caller wrote, so t=,10 and t=0,10 both ask
// the upstream for t=1,11. A query with no t= asks the upstream for
// none, which is the upstream's own default, an unbounded capture from
// its zero.
func (q captureQuery) shiftedTemporal(lead float64) string {
	if !q.HasBegin && !q.HasEnd {
		return ""
	}
	begin := formatNPT(q.Begin + lead)
	if !q.HasEnd {
		return begin
	}
	return begin + "," + formatNPT(q.End+lead)
}

// formatNPT writes a time in the shortest form that reads back as the
// same number, so t=0,10 stays 0,10 in a Location and 1.5 does not
// become 1.500000.
func formatNPT(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}
