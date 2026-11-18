package main

// This file parses the query of a capture route: the t= and xywh=
// dimensions of W3C Media Fragments 1.0, and the width=, height=,
// framerate=, quality=, and bitrate= parameters. The specification
// tells a user agent to ignore an invalid, unknown, or non-existent
// dimension (sections 6.2, 6.2.1, and 6.3.1). This API responds with
// 400 instead, because a query produces a new resource (sections 3.1
// and 7.4), and a client that asked for a region must not silently get
// the whole screen.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// captureQuery is a parsed capture query. The times are numbers,
// because the composed route adds the lead-in to them and compares
// begin with end. The other parameters stay strings, because this API
// only checks their form and passes them to the display or audio API
// that interprets them.
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

// captureBeginMax is the largest t= begin, in seconds. A sidecar runs
// its pipeline and discards frames or samples until the begin time, so
// each second of begin costs a second of work for no output. The
// timeout for upstream response headers also grows by the begin time.
const captureBeginMax = 60.0

// The query keys that each aspect accepts. Each key goes to the
// upstream API of the aspect, so a key that the upstream rejects, such
// as bitrate= for the display API, is refused here.
var (
	screenCaptureKeys = []string{"t", "xywh", "width", "height", "framerate", "quality"}
	audioCaptureKeys  = []string{"t", "bitrate"}
	mediaCaptureKeys  = []string{"t", "xywh", "width", "height", "framerate", "quality", "bitrate"}
)

// parseCaptureQuery parses the raw query as Media Fragments section
// 5.1.1 describes: it splits on & and on the first =, and then
// percent-decodes each name and value. So t=10%2C20 is the interval
// 10,20, and t=%6ept:10 is npt:10. It does not use url.ParseQuery,
// which accepts a key with no = and decodes + as a space.
func parseCaptureQuery(rawQuery string, allowed []string) (captureQuery, error) {
	var query captureQuery
	var seen []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawName, rawValue, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			return captureQuery{}, fmt.Errorf("the query part %q has no =", pair)
		}
		name, err := url.PathUnescape(rawName)
		if err != nil {
			return captureQuery{}, fmt.Errorf("decoding the query key %q: %w", rawName, err)
		}
		value, err := url.PathUnescape(rawValue)
		if err != nil {
			return captureQuery{}, fmt.Errorf("decoding the %s= value %q: %w", name, rawValue, err)
		}
		if !slices.Contains(allowed, name) {
			return captureQuery{}, fmt.Errorf("the query key %q is not one of the allowed keys: %s", name, strings.Join(allowed, ", "))
		}
		if slices.Contains(seen, name) {
			return captureQuery{}, fmt.Errorf("the query repeats the key %q", name)
		}
		seen = append(seen, name)
		if err := query.setDimension(name, value); err != nil {
			return captureQuery{}, err
		}
	}
	// A capture takes width= or height= and scales the other side to
	// keep the aspect ratio. Both together could contradict the ratio of
	// the frame, so this API refuses the pair instead of leaving the
	// sidecar to choose one.
	if query.Width != "" && query.Height != "" {
		return captureQuery{}, fmt.Errorf("the query sets width=%s and height=%s, and a capture takes only one of them", query.Width, query.Height)
	}
	return query, nil
}

// setDimension parses one key and value into the query. A key that is
// in an allowed list but has no case here returns an error, so a key
// added to a list without a parser fails a test and does not silently
// do nothing.
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
	return fmt.Errorf("the query key %q has no parser", name)
}

// setTemporal parses t=: an optional npt: prefix, then a begin, an end,
// or begin,end in normal play time. The interval includes the begin
// and excludes the end (section 4.2.1), so an end at or before the
// begin is an empty interval and returns an error. A begin over
// captureBeginMax also returns an error.
func (q *captureQuery) setTemporal(value string) error {
	interval := value
	if len(value) >= 4 && strings.EqualFold(value[:4], "npt:") {
		interval = value[4:]
	}
	beginText, endText, hasEnd := strings.Cut(interval, ",")
	if beginText != "" {
		begin, err := parseNPT("t= begin", beginText)
		if err != nil {
			return err
		}
		if begin > captureBeginMax {
			return fmt.Errorf("the t= begin %s is over the limit of %s seconds", formatNPT(begin), formatNPT(captureBeginMax))
		}
		q.HasBegin, q.Begin = true, begin
	}
	if hasEnd && endText != "" {
		end, err := parseNPT("t= end", endText)
		if err != nil {
			return err
		}
		q.HasEnd, q.End = true, end
	}
	if !q.HasBegin && !q.HasEnd {
		return fmt.Errorf("the t= value %q has no begin and no end", value)
	}
	if q.HasBegin && q.HasEnd && q.Begin >= q.End {
		return fmt.Errorf("the t= end %s is not after the begin %s", formatNPT(q.End), formatNPT(q.Begin))
	}
	return nil
}

// parseNPT parses the three normal play time forms of section 4.2.1:
// ss, mm:ss, and hh:mm:ss, with an optional fraction on the seconds.
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

// parseSeconds parses the seconds field, the only field that can have
// a fraction. After a minutes field, the grammar requires two digits
// below sixty, so 1:75 is an error and not 135 seconds.
func parseSeconds(text string, afterMinutes bool) (float64, bool) {
	whole, fraction, _ := strings.Cut(text, ".")
	if !digits(whole) || !digitsOrEmpty(fraction) {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(text, "."), 64)
	if err != nil {
		return 0, false
	}
	if afterMinutes && (len(whole) != 2 || seconds >= 60) {
		return 0, false
	}
	return seconds, true
}

// parseWhole parses a minutes or hours field. The grammar requires the
// minutes field to be two digits below sixty, and puts no limit on the
// hours field.
func parseWhole(text string, isMinutes bool) (float64, bool) {
	if !digits(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	if isMinutes && (len(text) != 2 || value >= 60) {
		return 0, false
	}
	return value, true
}

// setSpatial validates xywh=, which is four whole numbers with an
// optional pixel: or percent: prefix, and keeps the value as written.
// This API has no frame size to check against, so it passes the region
// on, and the display sidecar clips a region that extends past the
// frame (section 6.1.2).
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

// setCount validates a parameter that must be a positive whole number,
// because a width or bitrate of zero is not a capture.
func setCount(target *string, name, value string) error {
	if !digits(value) || strings.TrimLeft(value, "0") == "" {
		return fmt.Errorf("the %s= value %q is not a positive whole number", name, value)
	}
	*target = value
	return nil
}

// digits reports whether text is one or more ASCII digits. It does not
// use strconv, which also accepts a sign, an exponent, an infinity, and
// a hexadecimal form, and none of those is a valid time or size.
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

// shiftedTemporal returns the t= value for an upstream request: the
// caller's interval with lead added to each end. The composed route
// passes a lead so that both sidecars have a running pipeline before
// the caller's time zero. An absent begin counts as zero, so t=,10
// with a lead of 1 gives 1,11. With no t= in the query it returns an
// empty string, and the upstream request has no t=.
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

// formatNPT formats a time with the fewest digits that parse back to
// the same number, so t=0,10 stays 0,10 in a Location header.
func formatNPT(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}
