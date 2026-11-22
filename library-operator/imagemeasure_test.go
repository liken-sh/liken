package main

// imagemeasure_test.go proves the three numbers of an image subtitle stream
// from bitmaps that the tests describe row by row, the way plan 97 defines
// each number.

import (
	"reflect"
	"testing"
)

// A bitmap on a 1080-row canvas whose top row is at canvas row top, with one
// line of text for each height, each line one row below the last.
func cueAt(top int, heights ...int) imageBitmap {
	bitmap := imageBitmap{canvasHeight: 1080, top: top}
	row := top
	for _, height := range heights {
		bitmap.lines = append(bitmap.lines, inkLine{start: row, end: row + height})
		row += height + 1
	}
	return bitmap
}

// The cues that a test hands the measurement, n of them at once.
func repeated(n int, bitmap imageBitmap) []imageBitmap {
	bitmaps := make([]imageBitmap, n)
	for i := range bitmaps {
		bitmaps[i] = bitmap
	}
	return bitmaps
}

func measuredFrom(bitmaps ...[]imageBitmap) *probedImage {
	var cues imageCues
	for _, group := range bitmaps {
		for _, bitmap := range group {
			cues.add(bitmap)
		}
	}
	return cues.measurement()
}

func TestTheMeasurementFollowsThePlansDefinitions(t *testing.T) {
	cases := []struct {
		name    string
		bitmaps [][]imageBitmap
		want    *probedImage
	}{
		{
			name:    "ten one-line bottom cues",
			bitmaps: [][]imageBitmap{repeated(10, cueAt(900, 60))},
			want:    &probedImage{CanvasHeight: 1080, LineHeight: 60, InkBottom: 960},
		},
		{
			// 18 lines of 60 rows and 2 of 70: the 90th percentile by nearest
			// rank is element ceil(0.9 × 20) − 1 = 17, which is 60.
			name: "the line height is the 90th percentile by nearest rank",
			bitmaps: [][]imageBitmap{
				repeated(8, cueAt(900, 60)), repeated(5, cueAt(830, 60, 60)), repeated(2, cueAt(900, 70)),
			},
			want: &probedImage{CanvasHeight: 1080, LineHeight: 60, InkBottom: 960},
		},
		{
			// Ten lines of 60 and ten of 48, so the percentile is 60. The short
			// last lines end at 948 and do not count, and the full ones end at
			// 960 and 970.
			name: "a last line shorter than the line height less one row does not count",
			bitmaps: [][]imageBitmap{
				repeated(10, cueAt(900, 48)), repeated(4, cueAt(900, 60)), repeated(6, cueAt(910, 60)),
			},
			want: &probedImage{CanvasHeight: 1080, LineHeight: 60, InkBottom: 970},
		},
		{
			// One row short of the line height still counts as a full line, so
			// the cues at 900 count beside the cues at 800.
			name:    "a last line one row short counts",
			bitmaps: [][]imageBitmap{repeated(10, cueAt(800, 60)), repeated(10, cueAt(900, 59))},
			want:    &probedImage{CanvasHeight: 1080, LineHeight: 60, InkBottom: 959},
		},
		{
			// Each line is 30 rows and the line height is 60, so no last line is
			// full, and every lower cue counts.
			name: "every lower cue counts when no last line is full",
			bitmaps: [][]imageBitmap{
				repeated(2, cueAt(100, 60)), repeated(4, cueAt(900, 30)), repeated(7, cueAt(920, 30)),
			},
			want: &probedImage{CanvasHeight: 1080, LineHeight: 60, InkBottom: 950},
		},
		{
			// The upper cues add their lines to the line height and nothing to
			// the ink bottom. A top row at half the canvas is a lower cue, and a
			// top row one above it is not.
			name: "an upper cue gives the line height and not the ink bottom",
			bitmaps: [][]imageBitmap{
				repeated(10, cueAt(540, 60)), repeated(30, cueAt(539, 70)),
			},
			want: &probedImage{CanvasHeight: 1080, LineHeight: 70, InkBottom: 600},
		},
		{
			name:    "nine lower cues are too few",
			bitmaps: [][]imageBitmap{repeated(9, cueAt(900, 60)), repeated(20, cueAt(100, 60))},
			want:    nil,
		},
		{
			name:    "a bitmap with no ink is not a cue",
			bitmaps: [][]imageBitmap{repeated(9, cueAt(900, 60)), repeated(5, cueAt(900))},
			want:    nil,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := measuredFrom(test.bitmaps...)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("measurement = %+v, want %+v", got, test.want)
			}
		})
	}
}

// The median is element n / 2 of the sorted values, so of an even count it is
// the upper of the two middle values.
func TestTheInkBottomOfAnEvenCountIsTheUpperMiddleValue(t *testing.T) {
	got := measuredFrom(repeated(5, cueAt(900, 60)), repeated(5, cueAt(901, 60)))

	if got == nil || got.InkBottom != 961 {
		t.Errorf("measurement = %+v, want an ink bottom of 961", got)
	}
}

// A bitmap is a row of alpha values per row. Rows of alpha 128 or more are
// ink, a run of them is a line, and a VobSub bitmap starts at its first row
// with any alpha, the way FFmpeg crops it.
func TestABitmapsLinesAreItsRunsOfInkRows(t *testing.T) {
	rows := []uint8{0, 0, 40, 200, 255, 128, 0, 127, 0, 255, 255, 0, 0}
	cases := []struct {
		name    string
		cropped bool
		want    imageBitmap
	}{
		{
			name: "a PGS object keeps its transparent margin",
			want: imageBitmap{canvasHeight: 1080, top: 900,
				lines: []inkLine{{start: 903, end: 906}, {start: 909, end: 911}}},
		},
		{
			name:    "a VobSub area starts at its first row with any alpha",
			cropped: true,
			want: imageBitmap{canvasHeight: 1080, top: 902,
				lines: []inkLine{{start: 903, end: 906}, {start: 909, end: 911}}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := bitmapOf(1080, 900, rows, test.cropped)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("bitmap = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestALineThatReachesTheLastRowEndsBelowIt(t *testing.T) {
	got := bitmapOf(480, 400, []uint8{0, 255, 255}, false)

	if want := []inkLine{{start: 401, end: 403}}; !reflect.DeepEqual(got.lines, want) {
		t.Errorf("lines = %+v, want %+v", got.lines, want)
	}
}
