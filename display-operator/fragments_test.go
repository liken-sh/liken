package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSelectionParsesTheTemporalExamples(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  timeRange
	}{
		{"a begin and an end", "t=10,20", timeRange{Begin: 10, End: 20, HasBegin: true, HasEnd: true}},
		{"an end alone", "t=,20", timeRange{End: 20, HasEnd: true}},
		{"a begin alone", "t=10", timeRange{Begin: 10, HasBegin: true}},
		{"the npt prefix", "t=npt:10,20", timeRange{Begin: 10, End: 20, HasBegin: true, HasEnd: true}},
		{"the npt prefix and an end alone", "t=npt:,121.5", timeRange{End: 121.5, HasEnd: true}},
		{"an end in hours, minutes, and seconds", "t=npt:20,0:02:01.5", timeRange{Begin: 20, End: 121.5, HasBegin: true, HasEnd: true}},
		{"a begin in hours, minutes, and seconds", "t=0:00:20,121.5", timeRange{Begin: 20, End: 121.5, HasBegin: true, HasEnd: true}},
		{"a begin in minutes and seconds", "t=00:20,121.5", timeRange{Begin: 20, End: 121.5, HasBegin: true, HasEnd: true}},
		{"a fractional begin", "t=10.5,20", timeRange{Begin: 10.5, End: 20, HasBegin: true, HasEnd: true}},
		{"a point and no fraction", "t=10.,20", timeRange{Begin: 10, End: 20, HasBegin: true, HasEnd: true}},
		{"the ceiling itself", "t=60,90", timeRange{Begin: 60, End: 90, HasBegin: true, HasEnd: true}},
		{"no temporal dimension", "", timeRange{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, refusal := parseSelection(test.query, "video/mp4")
			if refusal != nil || got.Time != test.want {
				t.Fatalf("parseSelection(%q) = %+v, %v, want %+v", test.query, got.Time, refusal, test.want)
			}
		})
	}
}

func TestSelectionParsesTheSpatialExamples(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  region
	}{
		{"the default unit", "xywh=160,120,320,240", region{X: 160, Y: 120, W: 320, H: 240}},
		{"the pixel unit", "xywh=pixel:160,120,320,240", region{X: 160, Y: 120, W: 320, H: 240}},
		{"the percent unit", "xywh=percent:25,25,50,50", region{Percent: true, X: 25, Y: 25, W: 50, H: 50}},
		{"a percent origin at zero", "xywh=percent:0,0,100,100", region{Percent: true, W: 100, H: 100}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, refusal := parseSelection(test.query, "video/mp4")
			if refusal != nil || got.Region == nil || *got.Region != test.want {
				t.Fatalf("parseSelection(%q) = %+v, %v, want %+v", test.query, got.Region, refusal, test.want)
			}
		})
	}
}

func TestSelectionDecodesAfterItSplits(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  timeRange
	}{
		{"an encoded key", "%74=10,20", timeRange{Begin: 10, End: 20, HasBegin: true, HasEnd: true}},
		{"encoded digits", "t=%31%30", timeRange{Begin: 10, HasBegin: true}},
		{"an encoded comma", "t=10%2C20", timeRange{Begin: 10, End: 20, HasBegin: true, HasEnd: true}},
		{"an encoded n", "t=%6ept:10", timeRange{Begin: 10, HasBegin: true}},
		{"an encoded colon", "t=npt%3a10", timeRange{Begin: 10, HasBegin: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, refusal := parseSelection(test.query, "video/mp4")
			if refusal != nil || got.Time != test.want {
				t.Fatalf("parseSelection(%q) = %+v, %v, want %+v", test.query, got.Time, refusal, test.want)
			}
		})
	}
}

func TestSelectionRefuses(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		mediaType string
		detail    string
	}{
		{"an unknown key", "zoom=2", "video/mp4", "zoom=2"},
		{"a repeated key", "t=10&t=20", "video/mp4", "t=20"},
		{"a repeated key with the same value", "width=640&width=640", "video/mp4", "width=640"},
		{"a time that is not a number", "t=asdf", "video/mp4", "t=asdf"},
		{"a time with a hyphen", "t=10-20", "video/mp4", "t=10-20"},
		{"three times", "t=10,20,40", "video/mp4", "t=10,20,40"},
		{"an end that is not a number", "t=5,ekj", "video/mp4", "t=5,ekj"},
		{"a begin that is not a number", "t=agk,9", "video/mp4", "t=agk,9"},
		{"a quoted time", "t='0'", "video/mp4", "t='0'"},
		{"an empty time", "t=", "video/mp4", "t="},
		{"a lone comma", "t=,", "video/mp4", "t=,"},
		{"an unknown time format", "t=smpte:10", "video/mp4", "t=smpte:10"},
		{"minutes of one digit", "t=0:2:00,121.5", "video/mp4", "t=0:2:00,121.5"},
		{"an equal begin and end", "t=10,10", "video/mp4", "t=10,10"},
		{"an end before the begin", "t=20,10", "video/mp4", "t=20,10"},
		{"a begin over the ceiling", "t=61", "video/mp4", "t=61"},
		{"a begin over the ceiling in minutes and seconds", "t=10:20", "video/mp4", "t=10:20"},
		{"the specification's hours, minutes, and seconds begin", "t=0:02:00,121.5", "video/mp4", "t=0:02:00,121.5"},
		{"the specification's npt begin", "t=npt:120,0:02:01.5", "video/mp4", "t=npt:120,0:02:01.5"},
		{"an end on a PNG", "t=,10", "image/png", "t=,10"},
		{"an end on a JPEG", "t=5,7", "image/jpeg", "t=5,7"},
		{"invalid percent-encoding", "t=%xy", "video/mp4", "t=%xy"},
		{"a region that is not four numbers", "xywh=4,5", "video/mp4", "xywh=4,5"},
		{"a region with a word in it", "xywh=4,5,abc,8", "video/mp4", "xywh=4,5,abc,8"},
		{"an unknown region unit", "xywh=foo:4,5,7,8", "video/mp4", "xywh=foo:4,5,7,8"},
		{"a percent over one hundred", "xywh=percent:400,5,6,8", "video/mp4", "xywh=percent:400,5,6,8"},
		{"a negative origin", "xywh=-1,0,10,10", "video/mp4", "xywh=-1,0,10,10"},
		{"a region of no width", "xywh=4,5,0,3", "video/mp4", "xywh=4,5,0,3"},
		{"a region of no height", "xywh=4,5,3,0", "video/mp4", "xywh=4,5,3,0"},
		{"a width with a height", "width=640&height=480", "video/mp4", "width=640"},
		{"a width of zero", "width=0", "video/mp4", "width=0"},
		{"a height of zero", "height=0", "video/mp4", "height=0"},
		{"a width that is not a number", "width=abc", "video/mp4", "width=abc"},
		{"a negative width", "width=-1", "video/mp4", "width=-1"},
		{"a fractional width", "width=640.5", "video/mp4", "width=640.5"},
		{"a framerate on a PNG", "framerate=30", "image/png", "framerate=30"},
		{"a framerate on a JPEG", "framerate=30", "image/jpeg", "framerate=30"},
		{"a framerate of zero", "framerate=0", "video/mp4", "framerate=0"},
		{"a quality on a PNG", "quality=90", "image/png", "quality=90"},
		{"a quality on an MP4", "quality=90", "video/mp4", "quality=90"},
		{"a quality of zero", "quality=0", "image/jpeg", "quality=0"},
		{"a quality over one hundred", "quality=101", "image/jpeg", "quality=101"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, refusal := parseSelection(test.query, test.mediaType)
			if refusal == nil || refusal.status != http.StatusBadRequest || !strings.Contains(refusal.detail, test.detail) {
				t.Fatalf("parseSelection(%q, %q) refused with %v, want 400 quoting %q",
					test.query, test.mediaType, refusal, test.detail)
			}
		})
	}
}

func TestSelectionDefaultsPerRepresentation(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		mediaType     string
		wantFramerate int
		wantQuality   int
	}{
		{"a PNG still", "", "image/png", 0, 0},
		{"a JPEG still", "", "image/jpeg", 0, 85},
		{"a clip", "", "video/mp4", 15, 0},
		{"a stream", "", "multipart/x-mixed-replace", 15, 85},
		{"a stated framerate", "framerate=30", "video/mp4", 30, 0},
		{"a stated quality", "quality=50", "image/jpeg", 0, 50},
		{"both stated on a stream", "framerate=5&quality=100", "multipart/x-mixed-replace", 5, 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, refusal := parseSelection(test.query, test.mediaType)
			if refusal != nil || got.Framerate != test.wantFramerate || got.Quality != test.wantQuality {
				t.Fatalf("parseSelection(%q, %q) = framerate %d, quality %d, %v, want %d and %d",
					test.query, test.mediaType, got.Framerate, got.Quality, refusal,
					test.wantFramerate, test.wantQuality)
			}
		})
	}
}

// regionSelection builds the selection a query would parse into, so
// a resolve row states its rectangle and nothing else.
func regionSelection(box region) captureSelection {
	return captureSelection{Region: &box}
}

func TestSelectionResolvesToWholePixels(t *testing.T) {
	tests := []struct {
		name        string
		chosen      captureSelection
		frameWidth  int
		frameHeight int
		want        cropRect
	}{
		{"no region", captureSelection{}, 1920, 1080, cropRect{W: 1920, H: 1080}},
		{"no region on an odd frame", captureSelection{}, 1919, 1081, cropRect{W: 1919, H: 1081}},
		{"the normal pixel case", regionSelection(region{X: 160, Y: 120, W: 320, H: 240}), 1920, 1080, cropRect{X: 160, Y: 120, W: 320, H: 240}},
		{"over the right edge", regionSelection(region{X: 1800, Y: 100, W: 300, H: 200}), 1920, 1080, cropRect{X: 1800, Y: 100, W: 120, H: 200}},
		{"over the bottom edge", regionSelection(region{X: 100, Y: 1000, W: 200, H: 200}), 1920, 1080, cropRect{X: 100, Y: 1000, W: 200, H: 80}},
		{"over both edges", regionSelection(region{X: 1800, Y: 1000, W: 300, H: 200}), 1920, 1080, cropRect{X: 1800, Y: 1000, W: 120, H: 80}},
		{"the normal percent case", regionSelection(region{Percent: true, X: 25, Y: 25, W: 50, H: 50}), 1920, 1080, cropRect{X: 480, Y: 270, W: 960, H: 540}},
		{"the whole frame in percent", regionSelection(region{Percent: true, W: 100, H: 100}), 1920, 1080, cropRect{W: 1920, H: 1080}},
		{"percent rounds the origin down and the size up", regionSelection(region{Percent: true, X: 10, Y: 10, W: 33, H: 33}), 1920, 1080, cropRect{X: 192, Y: 108, W: 634, H: 358}},
		{"percent over the edges", regionSelection(region{Percent: true, X: 80, Y: 80, W: 50, H: 50}), 1920, 1080, cropRect{X: 1536, Y: 864, W: 384, H: 216}},
		{"an odd width rounds up", regionSelection(region{W: 101, H: 100}), 1920, 1080, cropRect{W: 102, H: 100}},
		{"an odd height rounds up", regionSelection(region{W: 100, H: 101}), 1920, 1080, cropRect{W: 100, H: 102}},
		{"an origin that no longer fits moves back", regionSelection(region{X: 1919, W: 50, H: 100}), 1920, 1080, cropRect{X: 1918, W: 2, H: 100}},
		{"an origin outside the frame", regionSelection(region{X: 2000, Y: 1200, W: 50, H: 50}), 1920, 1080, cropRect{X: 1918, Y: 1078, W: 2, H: 2}},
		{"an odd frame rounds down", regionSelection(region{W: 1919, H: 1081}), 1919, 1081, cropRect{W: 1918, H: 1080}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.chosen.resolve(test.frameWidth, test.frameHeight)
			if got != test.want {
				t.Fatalf("resolve(%d, %d) = %+v, want %+v",
					test.frameWidth, test.frameHeight, got, test.want)
			}
		})
	}
}

func TestSelectionValidatesAgainstTheScreen(t *testing.T) {
	tests := []struct {
		name    string
		chosen  captureSelection
		refresh int
		detail  string
	}{
		{"nothing stated", captureSelection{}, 60, ""},
		{"a width the screen has", captureSelection{Width: 1920}, 60, ""},
		{"a width under the screen's", captureSelection{Width: 640}, 60, ""},
		{"a width over the screen's", captureSelection{Width: 3840}, 60, "width=3840"},
		{"a height the screen has", captureSelection{Height: 1080}, 60, ""},
		{"a height over the screen's", captureSelection{Height: 2160}, 60, "height=2160"},
		{"a framerate the screen refreshes at", captureSelection{Framerate: 60}, 60, ""},
		{"a framerate over the refresh", captureSelection{Framerate: 120}, 60, "framerate=120"},
		{"no refresh reported", captureSelection{Framerate: 120}, 0, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			refusal := test.chosen.validate(1920, 1080, test.refresh)
			if !refusalMatches(refusal, test.detail) {
				t.Fatalf("validate(1920, 1080, %d) = %v, want %q", test.refresh, refusal, test.detail)
			}
		})
	}
}

// An empty detail is a row that passes. Any other is a 400 whose
// words carry the offending value.
func refusalMatches(refusal *fault, detail string) bool {
	if detail == "" {
		return refusal == nil
	}
	return refusal != nil &&
		refusal.status == http.StatusBadRequest &&
		strings.Contains(refusal.detail, detail)
}

// expandTemplate is RFC 6570's form-style, label, and simple
// expansion, the three operators screenTemplate uses, so the drill
// expands the published template the way a client library would.
func expandTemplate(template string, values map[string]string) string {
	expanded := template
	for {
		open := strings.Index(expanded, "{")
		if open < 0 {
			return expanded
		}
		shut := open + strings.Index(expanded[open:], "}")
		expanded = expanded[:open] + expandExpression(expanded[open+1:shut], values) + expanded[shut+1:]
	}
}

func expandExpression(expression string, values map[string]string) string {
	operator := ""
	if strings.ContainsAny(expression[:1], ".?") {
		operator, expression = expression[:1], expression[1:]
	}
	parts := []string{}
	for _, name := range strings.Split(expression, ",") {
		value, stated := values[name]
		if !stated {
			continue
		}
		if operator == "?" {
			parts = append(parts, name+"="+encodeTemplateValue(value))
			continue
		}
		parts = append(parts, encodeTemplateValue(value))
	}
	if len(parts) == 0 {
		return ""
	}
	if operator == "?" {
		return "?" + strings.Join(parts, "&")
	}
	return operator + strings.Join(parts, ",")
}

// RFC 6570 leaves the unreserved characters of RFC 3986 alone and
// percent-encodes every other octet, commas and colons included.
func encodeTemplateValue(value string) string {
	const unreserved = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	encoded := &strings.Builder{}
	for _, octet := range []byte(value) {
		if strings.IndexByte(unreserved, octet) >= 0 {
			encoded.WriteByte(octet)
			continue
		}
		fmt.Fprintf(encoded, "%%%02X", octet)
	}
	return encoded.String()
}

func TestFragmentsRoundTripThroughTheTemplate(t *testing.T) {
	expanded := expandTemplate(screenTemplate, map[string]string{
		"name":  "HDMI-A-1",
		"ext":   "mp4",
		"t":     "5,7",
		"xywh":  "percent:25,25,50,50",
		"width": "480",
	})
	wantPath := "/v1/display/displays/HDMI-A-1/screen.mp4"
	wantQuery := "t=5%2C7&xywh=percent%3A25%2C25%2C50%2C50&width=480"
	if expanded != wantPath+"?"+wantQuery {
		t.Fatalf("expandTemplate(%q) = %q, want %q", screenTemplate, expanded, wantPath+"?"+wantQuery)
	}

	path, query, _ := strings.Cut(expanded, "?")
	got, refusal := parseSelection(query, "video/mp4")
	want := captureSelection{
		Time:      timeRange{Begin: 5, End: 7, HasBegin: true, HasEnd: true},
		Region:    &region{Percent: true, X: 25, Y: 25, W: 50, H: 50},
		Width:     480,
		Framerate: 15,
	}
	if refusal != nil || path != wantPath || got.Time != want.Time || *got.Region != *want.Region ||
		got.Width != want.Width || got.Framerate != want.Framerate || got.Quality != want.Quality {
		t.Fatalf("parseSelection(%q) = %+v, %v, want %+v", query, got, refusal, want)
	}

	// The expansion is then called, because the proof is that a
	// client which expands the published template reaches the
	// representation it named.
	server := newTestAPI(t, newTestCluster(t), newSidecarFixture(t))
	resp := call(t, server, http.MethodGet, expanded, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the expanded template answered %d: %s", resp.StatusCode, body(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != clipContentType {
		t.Errorf("the expanded template was served %q, want %q", got, clipContentType)
	}
}
