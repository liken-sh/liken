package main

import (
	"strings"
	"testing"
)

func TestCaptureQueryReadsAnEmptyQuery(t *testing.T) {
	got, err := parseCaptureQuery("", mediaCaptureKeys)

	mustSucceed(t, err)
	mustMatch(t, got, captureQuery{})
}

func TestCaptureQueryReadsTheTemporalDimension(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want captureQuery
	}{
		{"begin and end", "t=10,20", captureQuery{HasBegin: true, Begin: 10, HasEnd: true, End: 20}},
		{"begin alone", "t=10", captureQuery{HasBegin: true, Begin: 10}},
		{"end alone", "t=,20", captureQuery{HasEnd: true, End: 20}},
		{"an npt prefix", "t=npt:10,20", captureQuery{HasBegin: true, Begin: 10, HasEnd: true, End: 20}},
		{"an upper case npt prefix", "t=NPT:10", captureQuery{HasBegin: true, Begin: 10}},
		{"a fraction", "t=1.5,2.25", captureQuery{HasBegin: true, Begin: 1.5, HasEnd: true, End: 2.25}},
		{"a trailing point", "t=1.,2.", captureQuery{HasBegin: true, Begin: 1, HasEnd: true, End: 2}},
		{"minutes and seconds", "t=00:01,01:30.5", captureQuery{HasBegin: true, Begin: 1, HasEnd: true, End: 90.5}},
		{"hours, minutes, and seconds", "t=00:00:30,01:00:00", captureQuery{HasBegin: true, Begin: 30, HasEnd: true, End: 3600}},
		{"a begin at the limit", "t=60,70", captureQuery{HasBegin: true, Begin: 60, HasEnd: true, End: 70}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseCaptureQuery(c.raw, mediaCaptureKeys)

			mustSucceed(t, err)
			mustMatch(t, got, c.want)
		})
	}
}

// Media Fragments section 5.1.1 splits the query before it decodes
// the names and values.
func TestCaptureQuerySplitsBeforeItDecodes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want captureQuery
	}{
		{"an encoded comma", "t=10%2C20", captureQuery{HasBegin: true, Begin: 10, HasEnd: true, End: 20}},
		{"an encoded n", "t=%6ept:10", captureQuery{HasBegin: true, Begin: 10}},
		{"an encoded colon", "t=npt%3a10", captureQuery{HasBegin: true, Begin: 10}},
		{"an encoded key", "%74=10", captureQuery{HasBegin: true, Begin: 10}},
		{"an encoded prefix", "xywh=%70ixel:0,0,640,480", captureQuery{XYWH: "pixel:0,0,640,480"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseCaptureQuery(c.raw, mediaCaptureKeys)

			mustSucceed(t, err)
			mustMatch(t, got, c.want)
		})
	}
}

// A region that extends past the frame is kept, because the display
// sidecar clips it.
func TestCaptureQueryKeepsTheSpatialDimensionAsWritten(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"no prefix", "xywh=0,0,640,480", "0,0,640,480"},
		{"the pixel prefix", "xywh=pixel:160,120,640,480", "pixel:160,120,640,480"},
		{"the percent prefix", "xywh=percent:0,0,50,50", "percent:0,0,50,50"},
		{"a region past the frame", "xywh=percent:50,50,200,200", "percent:50,50,200,200"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseCaptureQuery(c.raw, mediaCaptureKeys)

			mustSucceed(t, err)
			mustMatch(t, got.XYWH, c.want)
		})
	}
}

func TestCaptureQueryKeepsTheParametersAsStrings(t *testing.T) {
	got, err := parseCaptureQuery("width=960&framerate=30&quality=80&bitrate=128000", mediaCaptureKeys)

	mustSucceed(t, err)
	mustMatch(t, got, captureQuery{Width: "960", Framerate: "30", Quality: "80", Bitrate: "128000"})
}

// Only width= and height= together are refused.
func TestCaptureQueryReadsHeightAlone(t *testing.T) {
	got, err := parseCaptureQuery("height=540", mediaCaptureKeys)

	mustSucceed(t, err)
	mustMatch(t, got, captureQuery{Height: "540"})
}

func TestCaptureQueryReadsEachRoutesOwnKeys(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		allowed []string
	}{
		{"the screen keys", "t=1,2&xywh=0,0,640,480&width=960&framerate=30&quality=80", screenCaptureKeys},
		{"the audio keys", "t=1,2&bitrate=128000", audioCaptureKeys},
		{"the media keys", "t=1,2&xywh=0,0,640,480&height=540&framerate=30&quality=80&bitrate=128000", mediaCaptureKeys},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseCaptureQuery(c.raw, c.allowed)

			mustSucceed(t, err)
		})
	}
}

func TestCaptureQueryRefusesAnInvalidQuery(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		allowed []string
	}{
		{"an unknown key", "zoom=2", mediaCaptureKeys},
		{"bitrate on a screen route", "bitrate=128000", screenCaptureKeys},
		{"xywh on an audio route", "xywh=0,0,640,480", audioCaptureKeys},
		{"width on an audio route", "width=960", audioCaptureKeys},
		{"a key with no equals sign", "t", mediaCaptureKeys},
		{"a repeated key", "t=1&t=2", mediaCaptureKeys},
		{"a key repeated under an encoding", "t=1&%74=2", mediaCaptureKeys},
		{"an equal begin and end", "t=10,10", mediaCaptureKeys},
		{"an end before the begin", "t=5,3", mediaCaptureKeys},
		{"a begin over the limit", "t=61,70", mediaCaptureKeys},
		{"width with height", "width=960&height=540", mediaCaptureKeys},
		{"an empty temporal value", "t=", mediaCaptureKeys},
		{"a comma with no times", "t=,", mediaCaptureKeys},
		{"an npt prefix with no time", "t=npt:", mediaCaptureKeys},
		{"a three part interval", "t=1,2,3", mediaCaptureKeys},
		{"a begin that is not a time", "t=abc,20", mediaCaptureKeys},
		{"an end that is not a time", "t=10,abc", mediaCaptureKeys},
		{"a negative time", "t=-5", mediaCaptureKeys},
		{"an exponent", "t=1e3", mediaCaptureKeys},
		{"a one digit minute field", "t=1:30", mediaCaptureKeys},
		{"a seconds field over 59", "t=00:60", mediaCaptureKeys},
		{"a minutes field over 59", "t=00:60:00", mediaCaptureKeys},
		{"an hours field that is not a number", "t=ab:00:30", mediaCaptureKeys},
		{"a four field time", "t=1:00:00:30", mediaCaptureKeys},
		{"a fraction that is not digits", "t=1.5x", mediaCaptureKeys},
		{"three spatial fields", "xywh=1,2,3", mediaCaptureKeys},
		{"an unknown spatial unit", "xywh=inch:0,0,640,480", mediaCaptureKeys},
		{"a negative spatial field", "xywh=0,0,-1,480", mediaCaptureKeys},
		{"an empty spatial value", "xywh=", mediaCaptureKeys},
		{"a width that is not a number", "width=abc", mediaCaptureKeys},
		{"a width of zero", "width=0", mediaCaptureKeys},
		{"a height with a sign", "height=+540", mediaCaptureKeys},
		{"a framerate with a fraction", "framerate=29.97", mediaCaptureKeys},
		{"an empty quality", "quality=", mediaCaptureKeys},
		{"an empty bitrate", "bitrate=", mediaCaptureKeys},
		{"a key that is not percent encoded", "%zz=10", mediaCaptureKeys},
		{"a value that is not percent encoded", "t=%zz", mediaCaptureKeys},
		{"a seconds value too large for a float64", "t=" + strings.Repeat("9", 400), mediaCaptureKeys},
		{"an hours value too large for a float64", "t=" + strings.Repeat("9", 400) + ":00:30", mediaCaptureKeys},
		{"an allowed key with no dimension", "zoom=2", []string{"zoom"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseCaptureQuery(c.raw, c.allowed)

			mustFail(t, err)
		})
	}
}

// Each error message quotes the part of the query that is wrong,
// because it becomes the detail of the problem document, and the
// client sees nothing else.
func TestCaptureQueryErrorsQuoteTheInvalidPart(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wanted string
	}{
		{"the unknown key", "zoom=2", "zoom"},
		{"the repeated key", "t=1&t=2", "t"},
		{"the reversed interval", "t=5,3", "3"},
		{"the begin over the limit", "t=61,70", "60"},
		{"both dimensions", "width=960&height=540", "height"},
		{"the refused spatial value", "xywh=1,2,3", "1,2,3"},
		{"the decoding error", "t=%zz", "invalid URL escape \"%zz\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseCaptureQuery(c.raw, mediaCaptureKeys)

			mustFail(t, err)
			mustMatch(t, strings.Contains(err.Error(), c.wanted), true)
		})
	}
}

func TestCaptureShiftedTemporalAddsTheLead(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"a begin and an end", "t=0,10", "1,11"},
		{"a begin alone", "t=5", "6"},
		{"an end alone", "t=,10", "1,11"},
		{"no temporal dimension", "width=960", ""},
		{"a fraction", "t=0.5,1.25", "1.5,2.25"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			query, err := parseCaptureQuery(c.raw, mediaCaptureKeys)
			mustSucceed(t, err)

			mustMatch(t, query.shiftedTemporal(1), c.want)
		})
	}
}

func TestCaptureFormatNPTWritesTheShortestValue(t *testing.T) {
	cases := []struct {
		name    string
		seconds float64
		want    string
	}{
		{"a whole number", 11, "11"},
		{"a half second", 1.5, "1.5"},
		{"zero", 0, "0"},
		{"a long fraction", 0.125, "0.125"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, formatNPT(c.seconds), c.want)
		})
	}
}
