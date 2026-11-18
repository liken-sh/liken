package main

// This file parses the query string of a capture request: t= and xywh=
// as W3C Media Fragments 1.0 (Recommendation, 25 September 2012)
// defines them in sections 4.2.1 and 4.2.2, and this API's own width,
// height, framerate, and quality.
//
// The values go in the query and not in the URL fragment, because
// section 3.1 says that a query produces a new resource and section 7.4
// allows a query to change the media type. So in
// screen.png?t=5&xywh=..., the extension chooses the media type and
// the query chooses the time and the region.
//
// Media Fragments tells a user agent to ignore an invalid, unknown, or
// non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API
// responds 400 instead, so a client that asked for a region never gets
// the whole screen by mistake. A region that runs past the edge of the
// screen is clipped as section 6.1.2 says, because the client still
// gets every pixel it asked for that exists.

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// captureBeginMax is the largest t= begin in seconds, the same limit
// as the media and audio capture APIs. The sidecar starts capturing
// when the request arrives and waits for the begin, so the limit bounds
// how long a request keeps a capture open before its first frame.
const captureBeginMax = 60.0

// timeRange is the half-open interval of section 4.2.1, with either
// end optional. t=5 begins at 5 seconds and runs until the client
// disconnects, and t=,10 begins now and ends at 10 seconds.
type timeRange struct {
	Begin, End       float64 // seconds
	HasBegin, HasEnd bool
}

// region is an xywh= rectangle. In pixel units it counts the physical
// pixels of the captured frame, not the logical pixels of a scaled
// output. In percent units it is a share of the frame, the unit that a
// Layout region uses.
type region struct {
	Percent    bool
	X, Y, W, H float64
}

// captureSelection is a capture request's parsed query, with the
// defaults of the requested media type.
type captureSelection struct {
	Time          timeRange
	Region        *region
	Width, Height int
	Framerate     int
	Quality       int
}

type cropRect struct{ X, Y, W, H int }

// parseSelection parses a capture request's query. The media type
// decides which parameters are valid and what their defaults are.
func parseSelection(rawQuery, mediaType string) (captureSelection, *fault) {
	chosen := captureSelection{
		Framerate: defaultFramerate(mediaType),
		Quality:   defaultQuality(mediaType),
	}
	// Section 5.1.1 splits on & and on the first = before it
	// percent-decodes, so t=10%2C20 and t=npt%3a10 parse the same as
	// t=10,20 and t=npt:10.
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

// check rejects the combinations that are invalid for the media type
// alone. validate checks the rest once the screen's size and refresh
// rate are known.
func (s captureSelection) check(mediaType string, given map[string]string) *fault {
	still := stillForm(mediaType)
	_, hasWidth := given["width"]
	_, hasHeight := given["height"]
	_, hasFramerate := given["framerate"]
	_, hasQuality := given["quality"]
	switch {
	case hasWidth && hasHeight:
		return badRequest("width=" + given["width"] + " and height=" + given["height"] + " are both set, and a request may set only one of them")
	case hasWidth && s.Width == 0:
		return badRequest("width=" + given["width"] + " is not a size")
	case hasHeight && s.Height == 0:
		return badRequest("height=" + given["height"] + " is not a size")
	case still && s.Time.HasEnd:
		return badRequest("t=" + given["t"] + " sets an end time, and " + mediaType + " is a single frame")
	case s.Time.HasBegin && s.Time.Begin > captureBeginMax:
		return badRequest("t=" + given["t"] + " begins after " +
			strconv.FormatFloat(captureBeginMax, 'f', -1, 64) + " seconds")
	case still && hasFramerate:
		return badRequest("framerate=" + given["framerate"] + " does not apply to " + mediaType + ", which is a single frame")
	case hasFramerate && s.Framerate == 0:
		return badRequest("framerate=" + given["framerate"] + " is not a rate")
	case hasQuality && (mediaType == "image/png" || mediaType == "video/mp4"):
		return badRequest("quality=" + given["quality"] + " does not apply to " + mediaType)
	case hasQuality && (s.Quality < 1 || s.Quality > 100):
		return badRequest("quality=" + given["quality"] + " is outside 1 to 100")
	}
	return nil
}

// defaultFramerate is 15 for a clip or a stream, because the sidecar
// copies and encodes every whole frame, and 15 halves the work of 30.
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

// parseNPT parses the npttimedef production of section 4.2.1: an
// optional npt: prefix, then a begin, an end, or both.
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
	// Section 6.2.2 makes an interval that ends at or before its begin
	// an error.
	if span.HasBegin && span.HasEnd && span.Begin >= span.End {
		return timeRange{}, false
	}
	return span, true
}

// parseNPTTime parses the npttime forms of section 4.2.1: seconds,
// mm:ss, and h:mm:ss. Minutes are exactly two digits and under 60, and
// seconds are under 60 when minutes are present.
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

// parseSeconds parses the npt-sec production, which allows a point
// with no fraction, as in "10.".
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

// parseRegion parses the xywhparam production of section 4.2.2. With
// no pixel: or percent: prefix, the numbers are pixels.
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
	// Section 6.2.3 makes a zero width or height an error.
	if box.W == 0 || box.H == 0 {
		return region{}, false
	}
	if box.Percent && (box.X > 100 || box.Y > 100 || box.W > 100 || box.H > 100) {
		return region{}, false
	}
	return box, true
}

// parseRegionNumber accepts a decimal part only for percent, so a
// fraction from a Layout region converts without rounding.
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

func parseCount(value string) (int, bool) {
	if !allDigits(value) {
		return 0, false
	}
	count, err := strconv.Atoi(value)
	return count, err == nil
}

// resolve converts the selection's region to whole pixels of a frame
// of the given size, or returns the whole frame when there is no
// region.
func (s captureSelection) resolve(frameWidth, frameHeight int) cropRect {
	if s.Region == nil {
		return cropRect{W: frameWidth, H: frameHeight}
	}
	box := *s.Region
	x, y := int(box.X), int(box.Y)
	w, h := int(box.W), int(box.H)
	// Section 6.1.2's formula divides by the frame size where it means
	// to multiply, so this follows its intent. The origin rounds down
	// and the size rounds up, so the region never loses a pixel the
	// client asked for.
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

// fitAxis clips a region to the frame along one axis, as section 6.1.2
// says. The encoder rejects odd dimensions, so the size also rounds up
// to an even number, and the origin moves back when that no longer
// fits. On a frame of odd size, the size rounds down to even.
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

// validate checks the parameters that depend on the screen's current
// mode. width= and height= only scale down, so a size over the
// screen's is a 400. When the screen reports no size or no refresh
// rate, the matching parameter is accepted.
func (s captureSelection) validate(frameWidth, frameHeight, refresh int) *fault {
	switch {
	case frameWidth > 0 && s.Width > frameWidth:
		return badRequest("width=" + strconv.Itoa(s.Width) + " is larger than the screen's width of " + strconv.Itoa(frameWidth))
	case frameHeight > 0 && s.Height > frameHeight:
		return badRequest("height=" + strconv.Itoa(s.Height) + " is larger than the screen's height of " + strconv.Itoa(frameHeight))
	case refresh > 0 && s.Framerate > refresh:
		return badRequest("framerate=" + strconv.Itoa(s.Framerate) + " is above the screen's refresh rate of " + strconv.Itoa(refresh))
	}
	return s.regionFits(frameWidth, frameHeight)
}

// regionFits rejects a region whose origin is at or past the edge of
// the screen. The region holds no pixel of the screen, and fitAxis
// would return a strip along the edge in its place.
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
