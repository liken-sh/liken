package main

// imagesubtitlepackets_test.go encodes the packets of image subtitle
// streams for the tests. No FFmpeg encoder writes PGS, and a test must not
// carry the bytes of a real disc, so these builders write PGS display sets
// and DVD sub-pictures from pictures that the tests describe.

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// A band of rows of one palette entry in a test's picture, from its first
// row to the row just below its last. A band is text unless it is a box.
type band struct {
	start int
	end   int
	entry uint8
	// The band fills its rows with no gap, as a box behind the text or a
	// picture does.
	box bool
}

// picture draws a picture of entries 0 with each band across its middle
// half. Each row of the picture is one row of palette entries. A band of
// text is strokes 4 pixels wide with gaps of 4 pixels between them, so each
// of its rows has clear pixels between its ink, as a row of letters has.
func picture(width, height int, bands ...band) [][]uint8 {
	rows := make([][]uint8, height)
	for y := range rows {
		rows[y] = make([]uint8, width)
		for _, b := range bands {
			if y >= b.start && y < b.end {
				for x := width / 4; x < width*3/4; x++ {
					if b.box || (x-width/4)%8 < 4 {
						rows[y][x] = b.entry
					}
				}
			}
		}
	}
	return rows
}

// The runs of one row of a picture, as pairs of an entry and a length.
func runsOf(row []uint8) [][2]int {
	var runs [][2]int
	for x := 0; x < len(row); {
		length := 1
		for x+length < len(row) && row[x+length] == row[x] {
			length++
		}
		runs = append(runs, [2]int{int(row[x]), length})
		x += length
	}
	return runs
}

// pgsRLE encodes a picture as a PGS object's run-length code: a byte that
// is not zero is one pixel of that entry, and a zero byte starts a run or
// ends a line.
func pgsRLE(rows [][]uint8) []byte {
	var out []byte
	for _, row := range rows {
		for _, run := range runsOf(row) {
			entry, length := byte(run[0]), run[1]
			switch {
			case entry != 0 && length <= 2:
				for range length {
					out = append(out, entry)
				}
			case entry == 0 && length < 64:
				out = append(out, 0, byte(length))
			case entry == 0:
				out = append(out, 0, 0x40|byte(length>>8), byte(length))
			case length < 64:
				out = append(out, 0, 0x80|byte(length), entry)
			default:
				out = append(out, 0, 0xc0|byte(length>>8), byte(length), entry)
			}
		}
		out = append(out, 0, 0)
	}
	return out
}

// One object of a PGS composition, at its place on the canvas.
type pgsTestObject struct {
	id      uint16
	x, y    int
	picture [][]uint8
}

// One PGS display set: a presentation composition, a palette, the objects,
// and the end segment. The zero value of each option is the usual case:
// an epoch start on a 1920×1080 canvas, palette 0 with entry 1 opaque and
// entry 2 at alpha 100, and every object defined in one segment.
type pgsTestSet struct {
	canvasHeight int
	state        byte
	alpha        map[uint8]uint8
	objects      []pgsTestObject
	// The composition names its objects and the set does not define them.
	undefined bool
	// Each object is defined in two segments, the way a large object is.
	split bool
	// The composition crops each object to its whole size.
	cropped bool
}

var usualPGSAlpha = map[uint8]uint8{0: 0, 1: 255, 2: 100}

func segment(kind byte, payload []byte) []byte {
	return append([]byte{kind, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}

func (s pgsTestSet) packet() []byte {
	height, alpha, state := s.canvasHeight, s.alpha, s.state
	if height == 0 {
		height = 1080
	}
	if alpha == nil {
		alpha = usualPGSAlpha
	}
	if state == 0 && !s.undefined {
		state = 0x80
	}
	composition := []byte{0x07, 0x80, byte(height >> 8), byte(height), 0x10, 0, 1, state, 0, 0, byte(len(s.objects))}
	for _, object := range s.objects {
		var flags byte
		if s.cropped {
			flags = 0x80
		}
		composition = binary.BigEndian.AppendUint16(composition, object.id)
		composition = append(composition, 0, flags)
		composition = binary.BigEndian.AppendUint16(composition, uint16(object.x))
		composition = binary.BigEndian.AppendUint16(composition, uint16(object.y))
		if s.cropped {
			composition = append(composition, 0, 0, 0, 0)
			composition = binary.BigEndian.AppendUint16(composition, uint16(len(object.picture[0])))
			composition = binary.BigEndian.AppendUint16(composition, uint16(len(object.picture)))
		}
	}
	out := segment(0x16, composition)
	palette := []byte{0, 0}
	for entry := range 256 {
		if a, set := alpha[uint8(entry)]; set {
			palette = append(palette, byte(entry), 235, 128, 128, a)
		}
	}
	out = append(out, segment(0x14, palette)...)
	for _, object := range s.objects {
		if !s.undefined {
			out = append(out, pgsObjectSegments(object, s.split)...)
		}
	}
	return append(out, segment(0x80, nil)...)
}

// The segments that define one object. The length in the first segment
// counts the width and the height, as a disc writes it.
func pgsObjectSegments(object pgsTestObject, split bool) []byte {
	rle := pgsRLE(object.picture)
	width, height := len(object.picture[0]), len(object.picture)
	head := binary.BigEndian.AppendUint16(nil, object.id)
	first := append(head, 0, 0xc0)
	first = append(first, byte((len(rle)+4)>>16), byte((len(rle)+4)>>8), byte(len(rle)+4))
	first = binary.BigEndian.AppendUint16(first, uint16(width))
	first = binary.BigEndian.AppendUint16(first, uint16(height))
	if !split {
		return segment(0x15, append(first, rle...))
	}
	half := len(rle) / 2
	first[3] = 0x80
	last := append(binary.BigEndian.AppendUint16(nil, object.id), 0, 0x40)
	return append(segment(0x15, append(first, rle[:half]...)), segment(0x15, append(last, rle[half:]...))...)
}

// The display set that clears the screen: a composition with no object.
func pgsClear() []byte {
	return append(segment(0x16, []byte{0x07, 0x80, 0x04, 0x38, 0x10, 0, 2, 0, 0, 0, 0}), segment(0x80, nil)...)
}

// One DVD sub-picture unit: a display area, the alpha of its four pixel
// codes, and the picture in codes 0 to 3. The picture is coded in two
// interlaced fields of 2-bit runs, the way a DVD stores it.
type spuTest struct {
	x, y    int
	alpha   [4]uint8
	picture [][]uint8
	// The unit states no alpha, so the decoder uses the last one it read.
	noAlpha bool
}

// A nibble writer for the 2-bit run-length code.
type nibbles struct {
	out  []byte
	half bool
}

func (n *nibbles) put(v byte) {
	if n.half {
		n.out[len(n.out)-1] |= v & 0x0f
	} else {
		n.out = append(n.out, v<<4)
	}
	n.half = !n.half
}

func (n *nibbles) align() { n.half = false }

// A run of length pixels of one code. The last run of a row is written as
// the code that fills the rest of the line, because a run of more than 255
// pixels has no other form.
func (n *nibbles) run(code byte, length int, toEnd bool) {
	v := length<<2 | int(code)
	switch {
	case toEnd:
		n.put(0)
		n.put(0)
		n.put(0)
		n.put(code)
	case v < 0x10:
		n.put(byte(v))
	case v < 0x40:
		n.put(byte(v >> 4))
		n.put(byte(v))
	case v < 0x100:
		n.put(0)
		n.put(byte(v >> 4))
		n.put(byte(v))
	default:
		n.put(0)
		n.put(byte(v >> 8))
		n.put(byte(v >> 4))
		n.put(byte(v))
	}
}

func spuField(rows [][]uint8, field int) []byte {
	var n nibbles
	for y := field; y < len(rows); y += 2 {
		runs := runsOf(rows[y])
		for i, run := range runs {
			n.run(byte(run[0]), run[1], i == len(runs)-1)
		}
		n.align()
	}
	return n.out
}

func (s spuTest) packet() []byte {
	top, bottom := spuField(s.picture, 0), spuField(s.picture, 1)
	width, height := len(s.picture[0]), len(s.picture)
	x2, y2 := s.x+width-1, s.y+height-1
	first := 4
	second := first + len(top)
	control := second + len(bottom)
	commands := []byte{0x01, 0x03, 0x32, 0x10}
	if !s.noAlpha {
		commands = append(commands, 0x04, s.alpha[3]<<4|s.alpha[2], s.alpha[1]<<4|s.alpha[0])
	}
	commands = append(commands, 0x05,
		byte(s.x>>4), byte(s.x<<4)|byte(x2>>8), byte(x2), byte(s.y>>4), byte(s.y<<4)|byte(y2>>8), byte(y2))
	commands = binary.BigEndian.AppendUint16(append(commands, 0x06), uint16(first))
	commands = binary.BigEndian.AppendUint16(commands, uint16(second))
	commands = append(commands, 0xff)
	stop := control + 4 + len(commands)
	sequences := binary.BigEndian.AppendUint16([]byte{0, 0}, uint16(stop))
	sequences = append(sequences, commands...)
	sequences = append(sequences, 0x00, 0x64)
	sequences = binary.BigEndian.AppendUint16(sequences, uint16(stop))
	sequences = append(sequences, 0x02, 0xff)
	size := control + len(sequences)
	out := binary.BigEndian.AppendUint16(nil, uint16(size))
	out = binary.BigEndian.AppendUint16(out, uint16(control))
	out = append(out, top...)
	out = append(out, bottom...)
	return append(out, sequences...)
}

// The usual alpha of a DVD sub-picture: code 0 is transparent, and codes 1
// to 3 are opaque.
var usualSPUAlpha = [4]uint8{0, 15, 15, 15}

// hexDump writes bytes the way ffprobe's -show_data writes them: lines of 16
// bytes in groups of two, after the offset and before the bytes as text.
func hexDump(data []byte) string {
	var out strings.Builder
	out.WriteString("\n")
	for offset := 0; offset < len(data); offset += 16 {
		line := data[offset:min(offset+16, len(data))]
		fmt.Fprintf(&out, "%08x: ", offset)
		i := 0
		for ; i < len(line); i++ {
			fmt.Fprintf(&out, "%02x", line[i])
			if i&1 == 1 {
				out.WriteString(" ")
			}
		}
		out.WriteString(strings.Repeat(" ", 41-2*i-i/2))
		for _, b := range line {
			if b-32 < 95 {
				out.WriteByte(b)
			} else {
				out.WriteByte('.')
			}
		}
		out.WriteString("\n")
	}
	return out.String()
}
