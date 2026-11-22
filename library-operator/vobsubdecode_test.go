package main

// vobsubdecode_test.go proves the VobSub decoder on DVD sub-pictures that
// the tests encode: the bitmap that comes out, cropped the way FFmpeg crops
// it, and the lines of each.

import (
	"reflect"
	"slices"
	"testing"
)

// The bitmaps of a stream of 480 rows that the decoder gave for each packet
// in turn.
func vobsubBitmaps(packets ...[]byte) []imageBitmap {
	decoder := vobsubDecoder{canvasHeight: 480}
	var bitmaps []imageBitmap
	for _, packet := range packets {
		bitmaps = append(bitmaps, decoder.decode(packet)...)
	}
	return append(bitmaps, decoder.finish()...)
}

// A display area of 60 rows with a transparent margin of 10 rows above its
// two lines of 16 and 14 rows. Rows 26 and 27 are an outline in code 2.
var twoLineArea = picture(300, 60,
	band{start: 10, end: 26, entry: 1}, band{start: 26, end: 28, entry: 2}, band{start: 30, end: 44, entry: 1})

func TestTheVobSubDecoderGivesTheAreaCroppedToItsFirstRowWithAlpha(t *testing.T) {
	cases := []struct {
		name    string
		packets [][]byte
		want    []imageBitmap
	}{
		{
			name:    "a two-line cue",
			packets: [][]byte{spuTest{x: 200, y: 380, alpha: usualSPUAlpha, picture: twoLineArea}.packet()},
			want:    []imageBitmap{shownAt(480, 390, inkLine{390, 408}, inkLine{410, 424})},
		},
		{
			// An outline of alpha 7 of 15 is below half, so it is not ink and
			// the first line ends above it. It still has alpha, so the crop
			// keeps it.
			name: "an outline of alpha 7",
			packets: [][]byte{
				spuTest{x: 200, y: 380, alpha: [4]uint8{0, 15, 7, 15}, picture: twoLineArea}.packet(),
			},
			want: []imageBitmap{shownAt(480, 390, inkLine{390, 406}, inkLine{410, 424})},
		},
		{
			name: "an outline of alpha 8",
			packets: [][]byte{
				spuTest{x: 200, y: 380, alpha: [4]uint8{0, 15, 8, 15}, picture: twoLineArea}.packet(),
			},
			want: []imageBitmap{shownAt(480, 390, inkLine{390, 408}, inkLine{410, 424})},
		},
		{
			// A unit that states no alpha uses the alpha that the unit before
			// it stated, because FFmpeg keeps it from one packet to the next.
			name: "a unit that states no alpha",
			packets: [][]byte{
				spuTest{x: 200, y: 380, alpha: [4]uint8{0, 15, 0, 15}, picture: twoLineArea}.packet(),
				spuTest{x: 200, y: 100, noAlpha: true, picture: twoLineArea}.packet(),
			},
			want: []imageBitmap{
				shownAt(480, 390, inkLine{390, 406}, inkLine{410, 424}),
				shownAt(480, 110, inkLine{110, 126}, inkLine{130, 144}),
			},
		},
		{
			name: "a unit split over two packets",
			packets: func() [][]byte {
				unit := spuTest{x: 200, y: 380, alpha: usualSPUAlpha, picture: twoLineArea}.packet()
				return [][]byte{unit[:40], unit[40:]}
			}(),
			want: []imageBitmap{shownAt(480, 390, inkLine{390, 408}, inkLine{410, 424})},
		},
		{
			name: "an area of one row",
			packets: [][]byte{
				spuTest{x: 200, y: 380, alpha: usualSPUAlpha, picture: picture(300, 1, band{start: 0, end: 1, entry: 1})}.packet(),
			},
			want: nil,
		},
		{
			// FFmpeg gives no subtitle for an area with no alpha.
			name: "an area with no alpha",
			packets: [][]byte{
				spuTest{x: 200, y: 380, alpha: [4]uint8{}, picture: twoLineArea}.packet(),
			},
			want: nil,
		},
		{
			// FFmpeg keeps the bitmap of the sequences it read before a
			// sequence that points back to an earlier one.
			name: "a control sequence that points back",
			packets: func() [][]byte {
				unit := spuTest{x: 200, y: 380, alpha: usualSPUAlpha, picture: twoLineArea}.packet()
				return [][]byte{append(unit[:len(unit)-4], 0, 0, 0xff, 0xff)}
			}(),
			want: []imageBitmap{shownAt(480, 390, inkLine{390, 408}, inkLine{410, 424})},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := vobsubBitmaps(test.packets...)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("bitmaps = %+v, want %+v", got, test.want)
			}
		})
	}
}

// Every length of run that the 2-bit code has a form for: one nibble for
// runs up to 3, two up to 15, three up to 63, four up to 255, and the code
// that fills the rest of the line.
func TestEveryRunOfTheVobSubCodeReadsBack(t *testing.T) {
	cases := []struct {
		name string
		row  []uint8
		want bitmapRow
	}{
		{name: "a run of 3", row: append(pixelRun(1, 3), make([]uint8, 5)...), want: bitmapRow{ink: 3, first: 0, last: 2}},
		{name: "a run of 15", row: append(pixelRun(1, 15), make([]uint8, 5)...), want: bitmapRow{ink: 15, first: 0, last: 14}},
		{name: "a run of 63", row: append(pixelRun(1, 63), make([]uint8, 5)...), want: bitmapRow{ink: 63, first: 0, last: 62}},
		{
			name: "a run of 255", row: append(pixelRun(1, 255), make([]uint8, 5)...),
			want: bitmapRow{ink: 255, first: 0, last: 254},
		},
		{
			name: "a run to the end of the line", row: append(make([]uint8, 5), pixelRun(1, 400)...),
			want: bitmapRow{ink: 400, first: 5, last: 404},
		},
		{
			name: "two runs with a gap between them", row: slices.Concat(pixelRun(1, 3), make([]uint8, 2), pixelRun(1, 4)),
			want: bitmapRow{ink: 7, first: 0, last: 8},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			area := [][]uint8{test.row, make([]uint8, len(test.row))}
			unit := spuTest{x: 0, y: 300, alpha: usualSPUAlpha, picture: area}.packet()
			decoder := vobsubDecoder{canvasHeight: 480}
			rows := make([]bitmapRow, 2)

			decoder.alpha = usualSPUAlpha
			decoder.field(unit, 4, len(test.row), rows, 0)

			test.want.alpha = 255
			if !reflect.DeepEqual(rows[0], test.want) {
				t.Errorf("row = %+v, want %+v", rows[0], test.want)
			}
		})
	}
}

// A cue that is a box, an opaque rectangle with the text cut out of it or
// drawn on it, has no line of text, so the measurement does not count it.
// Some DVDs draw every line of every cue this way.
func TestAVobSubBoxHasNoLineOfText(t *testing.T) {
	box := picture(300, 40, band{start: 0, end: 30, entry: 3, box: true})

	got := vobsubBitmaps(spuTest{x: 200, y: 400, alpha: usualSPUAlpha, picture: box}.packet())

	if want := []imageBitmap{shownAt(480, 400)}; !reflect.DeepEqual(got, want) {
		t.Errorf("bitmaps = %+v, want %+v", got, want)
	}
}

// A damaged unit gives no bitmap, the way FFmpeg discards it, and never
// reads past its end.
func TestADamagedVobSubUnitGivesNoBitmap(t *testing.T) {
	whole := spuTest{x: 200, y: 380, alpha: usualSPUAlpha, picture: twoLineArea}.packet()
	withControl := func(control ...byte) []byte {
		unit := append([]byte{0, 0, 0, 4, 0, 0, 0, 4}, control...)
		unit[1] = byte(len(unit))
		return unit
	}
	cases := []struct {
		name   string
		packet []byte
	}{
		{name: "a unit of nine bytes", packet: []byte{0, 9, 0, 4, 0, 0, 0, 4, 0xff}},
		{name: "a unit whose end never arrives", packet: whole[:len(whole)-1]},
		{name: "a unit with four-byte offsets", packet: append([]byte{0, 0}, whole...)},
		{name: "an alpha command cut short", packet: withControl(0x04, 0xff)},
		{name: "a colour command cut short", packet: withControl(0x03, 0xff)},
		{name: "an area command cut short", packet: withControl(0x05, 0, 0, 0, 0, 0xff)},
		{name: "an offsets command cut short", packet: withControl(0x06, 0, 0, 0xff)},
		{name: "offsets past the end", packet: withControl(0x05, 0, 0, 9, 0, 0, 9, 0x06, 0x0f, 0, 0x0f, 0, 0xff)},
		{name: "a run longer than the row", packet: withControl(0x05, 0, 0, 1, 0, 0, 1, 0x06, 0, 3, 0, 3, 0xff)},
		{name: "a sequence with no end command", packet: withControl(0x01)},
		{
			// The last byte starts a run whose third nibble is past the end, so
			// the next run of the row starts past the end of the unit.
			name:   "a field whose code ends before its row does",
			packet: withControl(0x05, 0, 0x01, 0xff, 0, 0, 0x01, 0x06, 0, 21, 0, 21, 0xff, 0x01),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := vobsubBitmaps(test.packet); len(got) != 0 {
				t.Errorf("bitmaps = %+v, want none", got)
			}
		})
	}
}
