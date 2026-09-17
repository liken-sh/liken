package main

// This file holds the query a capture request carries: t= and xywh=
// from W3C Media Fragments 1.0 (Recommendation, 25 September 2012),
// sections 4.2.1 and 4.2.2, and this API's own width, height,
// framerate, and quality.
//
// The dimensions go in the query and not in the fragment. Section
// 3.1 says "a URI query produces a new resource, while a URI fragment
// provides a secondary resource", and section 7.4 says that with the
// query form "media type changes are possible", which is
// screen.png?t=5&xywh=... in one sentence.
//
// Media Fragments tells a user agent to ignore an invalid, unknown,
// or non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API
// answers 400 instead, because a query produces a new resource and a
// client that asked for a region must not silently get the whole
// screen. An out-of-range xywh is clipped per section 6.1.2, not
// refused, because the client still gets the pixels it named.

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// The most seconds a t= begin may name, the same number in all three
// of liken's capture APIs. The sidecar starts its pipeline at once
// and discards frames until this many seconds have passed on its own
// clock, so a begin is a bound on how long a request holds an output
// before it produces a byte.
const captureBeginMax = 60.0

// The half-open interval of Media Fragments section 4.2.1. Either
// side may be absent: t=5 begins at five and runs until hang-up, and
// t=,10 begins now and ends at ten.
type timeRange struct {
	Begin, End       float64 // seconds
	HasBegin, HasEnd bool
}

// A region in one of the two units of section 4.2.2. pixel: counts
// the frame's own pixels, the physical pixels weston_capture_source_v1
// reports, not the logical pixels a client draws at under scale=2.
// percent: is a share of the frame, the unit a Layout region maps to.
type region struct {
	Percent    bool
	X, Y, W, H float64
}

// One capture request's whole query, parsed and defaulted for the
// representation it is served as.
type captureSelection struct {
	Time          timeRange
	Region        *region
	Width, Height int
	Framerate     int
	Quality       int
}

// The rectangle the sidecar cuts out of a frame, in whole pixels of
// that frame.
type cropRect struct{ X, Y, W, H int }

// parseSelection parses one capture request's query for the
// representation it will be served as, since the knobs a form takes
// depend on the form.
func parseSelection(rawQuery, mediaType string) (captureSelection, *fault) {
	chosen := captureSelection{
		Framerate: defaultFramerate(mediaType),
		Quality:   defaultQuality(mediaType),
	}
	// Section 5.1.1's order: split on & and on the first =, then
	// percent-decode. An encoded comma or colon is then part of one
	// value and never a separator, so t=10%2C20 and t=npt%3a10 parse
	// as their literal forms.
	given := map[string]string{}
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, keyErr := url.PathUnescape(rawKey)
		value, valueErr := url.PathUnescape(rawValue)
		if keyErr != nil || valueErr != nil {
			return captureSelection{}, badRequest(pair + " is not valid percent-encoding")
		}
		if earlier, repeated := given[key]; repeated {
			return captureSelection{}, badRequest(key + "=" + value + " repeats " + key + "=" + earlier)
		}
		given[key] = value
		if refusal := chosen.take(key, value); refusal != nil {
			return captureSelection{}, refusal
		}
	}
	if refusal := chosen.check(mediaType, given); refusal != nil {
		return captureSelection{}, refusal
	}
	return chosen, nil
}

// An unknown key and a value that does not parse are both 400 here,
// where Media Fragments sections 6.2 and 6.2.1 tell a user agent to
// ignore them.
func (s *captureSelection) take(key, value string) *fault {
	switch key {
	case "t":
		span, ok := parseNPT(value)
		if !ok {
			return badRequest("t=" + value + " is not a Media Fragments time")
		}
		s.Time = span
		return nil
	case "xywh":
		box, ok := parseRegion(value)
		if !ok {
			return badRequest("xywh=" + value + " is not a Media Fragments region")
		}
		s.Region = &box
		return nil
	}
	knobs := map[string]*int{
		"width":     &s.Width,
		"height":    &s.Height,
		"framerate": &s.Framerate,
		"quality":   &s.Quality,
	}
	knob, known := knobs[key]
	if !known {
		return badRequest(key + "=" + value + " is not one of t, xywh, width, height, framerate, quality")
	}
	count, ok := parseCount(value)
	if !ok {
		return badRequest(key + "=" + value + " is not a whole number")
	}
	*knob = count
	return nil
}

// The checks one representation can answer on its own, before the
// screen's own numbers are known. Every one of them is a 400: width
// with height, a t= end or a framerate on a still, a begin past
// captureBeginMax, a quality on a form that has none, and a value
// outside its range.
func (s captureSelection) check(mediaType string, given map[string]string) *fault {
	still := stillForm(mediaType)
	_, hasWidth := given["width"]
	_, hasHeight := given["height"]
	_, hasFramerate := given["framerate"]
	_, hasQuality := given["quality"]
	switch {
	case hasWidth && hasHeight:
		return badRequest("width=" + given["width"] + " with height=" + given["height"])
	case hasWidth && s.Width == 0:
		return badRequest("width=" + given["width"] + " is not a size")
	case hasHeight && s.Height == 0:
		return badRequest("height=" + given["height"] + " is not a size")
	case still && s.Time.HasEnd:
		return badRequest("t=" + given["t"] + " names an end, and " + mediaType + " is one frame")
	case s.Time.HasBegin && s.Time.Begin > captureBeginMax:
		return badRequest("t=" + given["t"] + " begins after " +
			strconv.FormatFloat(captureBeginMax, 'f', -1, 64) + " seconds")
	case still && hasFramerate:
		return badRequest("framerate=" + given["framerate"] + " on " + mediaType + ", which is one frame")
	case hasFramerate && s.Framerate == 0:
		return badRequest("framerate=" + given["framerate"] + " is not a rate")
	case hasQuality && (mediaType == "image/png" || mediaType == "video/mp4"):
		return badRequest("quality=" + given["quality"] + " on " + mediaType)
	case hasQuality && (s.Quality < 1 || s.Quality > 100):
		return badRequest("quality=" + given["quality"] + " outside 1 to 100")
	}
	return nil
}

// The two defaults. A clip or stream runs at 15 frames per second,
// because every frame costs the sidecar a copy of the whole frame,
// and 15 halves the encode cost of the common request against 30.
// A JPEG is quality 85, at which a 1080p MJPEG frame is about
// 100 KB, 12 Mbit/s at 15 fps.
func defaultFramerate(mediaType string) int {
	if mediaType == "video/mp4" || mediaType == "multipart/x-mixed-replace" {
		return 15
	}
	return 0
}

func defaultQuality(mediaType string) int {
	if mediaType == "image/jpeg" || mediaType == "multipart/x-mixed-replace" {
		return 85
	}
	return 0
}

// The npttimedef production of section 4.2.1: an optional npt:
// prefix, then a begin, an end, or both. The end is the first instant
// outside the interval.
func parseNPT(value string) (timeRange, bool) {
	value = strings.TrimPrefix(value, "npt:")
	begin, end, split := strings.Cut(value, ",")
	if begin == "" && end == "" {
		return timeRange{}, false
	}
	span := timeRange{}
	if begin != "" {
		seconds, ok := parseNPTTime(begin)
		if !ok {
			return timeRange{}, false
		}
		span.Begin, span.HasBegin = seconds, true
	}
	if split && end != "" {
		seconds, ok := parseNPTTime(end)
		if !ok {
			return timeRange{}, false
		}
		span.End, span.HasEnd = seconds, true
	}
	// An interval that ends where it begins, or before it, selects
	// nothing (section 6.2.2).
	if span.HasBegin && span.HasEnd && span.Begin >= span.End {
		return timeRange{}, false
	}
	return span, true
}

// The three npttime forms of section 4.2.1: seconds alone,
// minutes:seconds, and hours:minutes:seconds. Minutes are exactly two
// digits under 60, and seconds are under 60 when minutes are given.
func parseNPTTime(value string) (float64, bool) {
	fields := strings.Split(value, ":")
	seconds, ok := parseSeconds(fields[len(fields)-1])
	if !ok {
		return 0, false
	}
	switch len(fields) {
	case 1:
		return seconds, true
	case 2:
		minutes, ok := parseTwoDigits(fields[0])
		return minutes*60 + seconds, ok && seconds < 60
	case 3:
		hours, ok := parseDigits(fields[0])
		minutes, minutesOK := parseTwoDigits(fields[1])
		return hours*3600 + minutes*60 + seconds, ok && minutesOK && seconds < 60
	}
	return 0, false
}

// The npt-sec production: whole digits, then an optional point and a
// fraction of any number of digits, the empty fraction of "10."
// included. Go's ParseFloat refuses a trailing point, so the point
// comes off before the parse.
func parseSeconds(value string) (float64, bool) {
	whole, fraction, _ := strings.Cut(value, ".")
	if !allDigits(whole) || (fraction != "" && !allDigits(fraction)) {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(value, "."), 64)
	return seconds, err == nil
}

func parseTwoDigits(value string) (float64, bool) {
	number, ok := parseDigits(value)
	return number, ok && len(value) == 2 && number < 60
}

func parseDigits(value string) (float64, bool) {
	if !allDigits(value) {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	return number, err == nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	return strings.IndexFunc(value, func(character rune) bool {
		return character < '0' || character > '9'
	}) < 0
}

// The xywhparam production of section 4.2.2: an optional pixel: or
// percent: prefix, then four numbers. A value with no prefix is in
// pixels.
func parseRegion(value string) (region, bool) {
	box := region{}
	switch {
	case strings.HasPrefix(value, "pixel:"):
		value = strings.TrimPrefix(value, "pixel:")
	case strings.HasPrefix(value, "percent:"):
		value, box.Percent = strings.TrimPrefix(value, "percent:"), true
	case strings.Contains(value, ":"):
		return region{}, false
	}
	fields := strings.Split(value, ",")
	if len(fields) != 4 {
		return region{}, false
	}
	numbers := [4]float64{}
	for i, field := range fields {
		number, ok := parseRegionNumber(field, box.Percent)
		if !ok {
			return region{}, false
		}
		numbers[i] = number
	}
	box.X, box.Y, box.W, box.H = numbers[0], numbers[1], numbers[2], numbers[3]
	// A region of no width or no height selects nothing (section
	// 6.2.3). A percent is a share of the frame, so no number in it
	// passes 100.
	if box.W == 0 || box.H == 0 {
		return region{}, false
	}
	if box.Percent && (box.X > 100 || box.Y > 100 || box.W > 100 || box.H > 100) {
		return region{}, false
	}
	return box, true
}

// pixel: counts whole pixels, and percent: takes a decimal, so the
// fraction a Layout region states maps to it without rounding.
func parseRegionNumber(field string, percent bool) (float64, bool) {
	if !percent {
		return parseDigits(field)
	}
	whole, fraction, split := strings.Cut(field, ".")
	if !allDigits(whole) || (split && !allDigits(fraction)) {
		return 0, false
	}
	number, err := strconv.ParseFloat(field, 64)
	return number, err == nil
}

// This API's own four knobs are unsigned whole numbers and nothing
// else: no sign, no point, no unit.
func parseCount(value string) (int, bool) {
	if !allDigits(value) {
		return 0, false
	}
	count, err := strconv.Atoi(value)
	return count, err == nil
}

// resolve turns the selection's region into whole pixels of a frame
// of this size. No region is the whole frame.
func (s captureSelection) resolve(frameWidth, frameHeight int) cropRect {
	if s.Region == nil {
		return cropRect{W: frameWidth, H: frameHeight}
	}
	box := *s.Region
	x, y := int(box.X), int(box.Y)
	w, h := int(box.W), int(box.H)
	// Section 6.1.2's percent case, whose published formula transposes
	// the operands: the origin rounds down and the size rounds up, so
	// the region never loses a pixel the client asked for.
	if box.Percent {
		x = int(math.Floor(box.X / 100 * float64(frameWidth)))
		y = int(math.Floor(box.Y / 100 * float64(frameHeight)))
		w = int(math.Ceil(box.W / 100 * float64(frameWidth)))
		h = int(math.Ceil(box.H / 100 * float64(frameHeight)))
	}
	x, w = fitAxis(x, w, frameWidth)
	y, h = fitAxis(y, h, frameHeight)
	return cropRect{X: x, Y: y, W: w, H: h}
}

// Section 6.1.2's clipping shortens a region that runs off an edge
// and never refuses it. The even rule is the one place this API adds
// a rule Media Fragments does not state: the encoder takes no odd
// dimension, so the size rounds up to even, an origin that no longer
// fits moves back, and a frame whose own size is odd rounds down
// instead.
func fitAxis(origin, size, frame int) (int, int) {
	if origin > frame-2 {
		origin = frame - 2
	}
	if origin < 0 {
		origin = 0
	}
	if origin+size > frame {
		size = frame - origin
	}
	size += size % 2
	if size > frame {
		size = frame - frame%2
	}
	if origin+size > frame {
		origin = frame - size
	}
	return origin, size
}

// validate checks the knobs only the screen's own mode can answer:
// the scale against the size, and the framerate against the refresh.
func (s captureSelection) validate(frameWidth, frameHeight, refresh int) *fault {
	// width= and height= scale down only, so a request for more
	// pixels than the screen has is a 400 and never an upscale. A
	// screen that reported no size and no refresh refuses no knob,
	// because the ceiling each one is measured against is not known.
	switch {
	case frameWidth > 0 && s.Width > frameWidth:
		return badRequest("width=" + strconv.Itoa(s.Width) + " over the screen's " + strconv.Itoa(frameWidth))
	case frameHeight > 0 && s.Height > frameHeight:
		return badRequest("height=" + strconv.Itoa(s.Height) + " over the screen's " + strconv.Itoa(frameHeight))
	case refresh > 0 && s.Framerate > refresh:
		return badRequest("framerate=" + strconv.Itoa(s.Framerate) + " over the screen's refresh of " + strconv.Itoa(refresh))
	}
	return s.regionFits(frameWidth, frameHeight)
}

// The one region section 6.1.2 clips to nothing: a rectangle whose
// origin is at or past an edge of the screen intersects no pixel.
// This API answers 400 rather than serve the strip of edge that
// clipping to a legal size would leave, for the reason every other
// refusal in this parser has: the client asked for a region, and
// must not silently get a different one.
func (s captureSelection) regionFits(frameWidth, frameHeight int) *fault {
	if s.Region == nil || frameWidth <= 0 || frameHeight <= 0 {
		return nil
	}
	x, y := int(s.Region.X), int(s.Region.Y)
	if s.Region.Percent {
		x, y = int(s.Region.X/100*float64(frameWidth)), int(s.Region.Y/100*float64(frameHeight))
	}
	if x >= frameWidth || y >= frameHeight {
		return badRequest(fmt.Sprintf("xywh= begins at %d,%d, outside the screen's %dx%d",
			x, y, frameWidth, frameHeight))
	}
	return nil
}
