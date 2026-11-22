package main

// pgsdecode_test.go proves the PGS decoder on display sets that the tests
// encode: the bitmaps that come out, at the place the composition gives
// them, and the lines of each.

import (
	"reflect"
	"slices"
	"testing"
)

// A cue of two lines, 60 rows and 50 rows, in an object 80 rows taller than
// its text, the way a disc leaves a transparent margin above and below.
var twoLineObject = picture(400, 200, band{start: 40, end: 100, entry: 1}, band{start: 110, end: 160, entry: 1})

// The bitmaps of a stream that the decoder gave for each packet in turn,
// and for the end of the window.
func pgsBitmaps(packets ...[]byte) []imageBitmap {
	var decoder pgsDecoder
	var bitmaps []imageBitmap
	for _, packet := range packets {
		bitmaps = append(bitmaps, decoder.decode(packet)...)
	}
	return append(bitmaps, decoder.finish()...)
}

func shownAt(canvasHeight, top int, lines ...inkLine) imageBitmap {
	return imageBitmap{canvasHeight: canvasHeight, top: top, lines: lines}
}

func TestThePGSDecoderGivesEachObjectAsMPVReceivesIt(t *testing.T) {
	cue := pgsTestObject{id: 0, x: 760, y: 820, picture: twoLineObject}
	other := pgsTestObject{id: 1, x: 760, y: 100, picture: picture(400, 60, band{start: 0, end: 60, entry: 1})}
	cases := []struct {
		name    string
		packets [][]byte
		want    []imageBitmap
	}{
		{
			name:    "one object, shown until a clear",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue}}.packet(), pgsClear()},
			want:    []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			name:    "an object still shown when the window ends",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue}}.packet()},
			want:    []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			name:    "two objects of one composition",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue, other}}.packet(), pgsClear()},
			want: []imageBitmap{
				shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980}),
				shownAt(1080, 100, inkLine{100, 160}),
			},
		},
		{
			name:    "an object defined in two segments",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue}, split: true}.packet(), pgsClear()},
			want:    []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			name: "a canvas of 576 rows",
			packets: [][]byte{
				pgsTestSet{canvasHeight: 576, objects: []pgsTestObject{{y: 400, picture: twoLineObject}}}.packet(),
			},
			want: []imageBitmap{shownAt(576, 400, inkLine{440, 500}, inkLine{510, 560})},
		},
		{
			// FFmpeg does not apply the cropping that a composition states,
			// so mpv receives the whole object.
			name:    "a composition that crops its object",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue}, cropped: true}.packet(), pgsClear()},
			want:    []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			name: "a composition that names more objects than it places",
			packets: func() [][]byte {
				set := pgsTestSet{objects: []pgsTestObject{cue}}.packet()
				set[3+10] = 2
				return [][]byte{set}
			}(),
			want: []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			// An acquisition point sends the same objects again, so a player
			// that starts there can draw them. It is the same cue.
			name: "the same objects again at an acquisition point",
			packets: [][]byte{
				pgsTestSet{objects: []pgsTestObject{cue}}.packet(),
				pgsTestSet{state: 0x40, objects: []pgsTestObject{cue}}.packet(),
				pgsClear(),
			},
			want: []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})},
		},
		{
			name: "a second cue with another picture",
			packets: [][]byte{
				pgsTestSet{objects: []pgsTestObject{cue}}.packet(),
				pgsTestSet{objects: []pgsTestObject{{x: 760, y: 900, picture: picture(400, 60, band{start: 0, end: 60, entry: 1})}}}.packet(),
			},
			want: []imageBitmap{
				shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980}),
				shownAt(1080, 900, inkLine{900, 960}),
			},
		},
		{
			// A composition that names an object the window never defined
			// starts in the middle of an epoch, and mpv draws nothing for it.
			name:    "an object the window never defined",
			packets: [][]byte{pgsTestSet{objects: []pgsTestObject{cue}, undefined: true}.packet(), pgsClear()},
			want:    nil,
		},
		{
			name: "a palette with no opaque entry",
			packets: [][]byte{
				pgsTestSet{alpha: map[uint8]uint8{0: 0, 1: 127}, objects: []pgsTestObject{cue}}.packet(), pgsClear(),
			},
			want: []imageBitmap{shownAt(1080, 820)},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := pgsBitmaps(test.packets...)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("bitmaps = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A fade in sends the same objects with a palette that grows more opaque.
// The cue is one bitmap, read with its most opaque palette, so its text is
// ink even though the first palette was faint.
func TestAFadeIsOneCueAtItsMostOpaquePalette(t *testing.T) {
	cue := pgsTestObject{x: 760, y: 820, picture: twoLineObject}

	got := pgsBitmaps(
		pgsTestSet{alpha: map[uint8]uint8{1: 40}, objects: []pgsTestObject{cue}}.packet(),
		pgsTestSet{alpha: map[uint8]uint8{1: 255}, objects: []pgsTestObject{cue}, undefined: true}.packet(),
		pgsTestSet{alpha: map[uint8]uint8{1: 90}, objects: []pgsTestObject{cue}, undefined: true}.packet(),
		pgsClear(),
	)

	want := []imageBitmap{shownAt(1080, 820, inkLine{860, 920}, inkLine{930, 980})}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bitmaps = %+v, want %+v", got, want)
	}
}

// Every form of a run that the code has, at the width and the length where
// each form starts: a single pixel, a short and a long run of entry 0, and a
// short and a long run of another entry. A faint pixel adds to the row's
// alpha and is not ink.
func TestEveryRunOfThePGSCodeReadsBack(t *testing.T) {
	cases := []struct {
		name string
		row  []uint8
		want bitmapRow
	}{
		{
			name: "one pixel after 63 transparent pixels", row: append(make([]uint8, 63), 1),
			want: bitmapRow{alpha: 255, ink: 1, first: 63, last: 63},
		},
		{
			name: "one pixel after 64 transparent pixels", row: append(make([]uint8, 64), 1),
			want: bitmapRow{alpha: 255, ink: 1, first: 64, last: 64},
		},
		{name: "a run of 63 opaque pixels", row: pixelRun(1, 63), want: bitmapRow{alpha: 255, ink: 63, first: 0, last: 62}},
		{name: "a run of 64 opaque pixels", row: pixelRun(1, 64), want: bitmapRow{alpha: 255, ink: 64, first: 0, last: 63}},
		{name: "a run of 2000 faint pixels", row: pixelRun(2, 2000), want: bitmapRow{alpha: 100}},
		{name: "a row of transparent pixels", row: make([]uint8, 500), want: bitmapRow{}},
		{
			name: "two runs with a gap between them", row: slices.Concat(pixelRun(1, 3), make([]uint8, 2), pixelRun(1, 4)),
			want: bitmapRow{alpha: 255, ink: 7, first: 0, last: 8},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			object := pgsObject{width: len(test.row), height: 1, rle: pgsRLE([][]uint8{test.row})}

			got := object.rows(&[256]uint8{1: 255, 2: 100})

			if !reflect.DeepEqual(got, []bitmapRow{test.want}) {
				t.Errorf("rows = %+v, want [%+v]", got, test.want)
			}
		})
	}
}

// A run past the object's width ends at its edge, so its pixels past the
// edge are not ink.
func TestAPGSRunPastTheWidthEndsAtTheEdge(t *testing.T) {
	object := pgsObject{width: 4, height: 1, rle: []byte{0, 0x80 | 6, 1}}

	got := object.rows(&[256]uint8{1: 255})

	if want := []bitmapRow{{alpha: 255, ink: 4, first: 0, last: 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

// A run whose length is zero names an entry and paints no pixel, so it adds
// nothing to the row, not even its alpha.
func TestAPGSRunOfNoPixelsAddsNothing(t *testing.T) {
	object := pgsObject{width: 4, height: 1, rle: []byte{0, 0x80, 1}}

	if got := object.rows(&[256]uint8{1: 255}); !reflect.DeepEqual(got, []bitmapRow{{}}) {
		t.Errorf("rows = %+v, want one empty row", got)
	}
}

// A cue that is a box, an opaque rectangle with the text cut out of it or
// drawn on it, has no line of text, so the measurement does not count it.
func TestAPGSBoxHasNoLineOfText(t *testing.T) {
	box := picture(400, 100, band{start: 20, end: 80, entry: 1, box: true})

	got := pgsBitmaps(pgsTestSet{objects: []pgsTestObject{{x: 760, y: 900, picture: box}}}.packet(), pgsClear())

	if want := []imageBitmap{shownAt(1080, 900)}; !reflect.DeepEqual(got, want) {
		t.Errorf("bitmaps = %+v, want %+v", got, want)
	}
}

func pixelRun(entry uint8, length int) []uint8 {
	row := make([]uint8, length)
	for i := range row {
		row[i] = entry
	}
	return row
}

// Code that ends after the zero byte of a run gives the pixels before it.
func TestPGSCodeThatEndsInARunGivesThePixelsBeforeIt(t *testing.T) {
	object := pgsObject{width: 4, height: 2, rle: []byte{1, 0}}

	want := []bitmapRow{{alpha: 255, ink: 1, first: 0, last: 0}, {}}
	if got := object.rows(&[256]uint8{1: 255}); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

// A damaged packet gives what it can and never reads past its end.
func TestADamagedPGSPacketGivesNoBitmap(t *testing.T) {
	whole := pgsTestSet{objects: []pgsTestObject{{y: 900, picture: twoLineObject}}}.packet()
	cases := []struct {
		name   string
		packet []byte
	}{
		{name: "a packet cut in the middle of a segment", packet: whole[:len(whole)/2]},
		{name: "a composition with too few bytes", packet: segment(0x16, []byte{0x07, 0x80, 0x04})},
		{name: "an object with too few bytes", packet: segment(0x15, []byte{0, 0, 0, 0xc0, 0, 0})},
		{name: "an object with no width", packet: segment(0x15, []byte{0, 0, 0, 0xc0, 0, 0, 4, 0, 0, 0, 1})},
		{name: "an end with no composition", packet: segment(0x80, nil)},
		{name: "a palette with no entry and no id", packet: segment(0x14, []byte{0})},
		{name: "an object segment of three bytes", packet: segment(0x15, []byte{0, 0, 0})},
		{name: "a continuation of an object never started", packet: segment(0x15, []byte{0, 9, 0, 0x40, 1, 2})},
		{name: "an empty packet", packet: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := pgsBitmaps(test.packet); len(got) != 0 {
				t.Errorf("bitmaps = %+v, want none", got)
			}
		})
	}
}
