package main

// vobsubdecode.go decodes the VobSub subtitle stream of a DVD into the
// bitmaps that mpv receives, for the measurement in imagemeasure.go. A
// VobSub packet is one sub-picture unit: the picture of a display area, in
// two interlaced fields of 2-bit run-length code, and control sequences that
// name the area, the offset of each field, and the alpha of each of the
// four pixel codes. FFmpeg's libavcodec/dvdsubdec.c decodes the same units
// for mpv, and this decoder follows it: the alpha of the last unit that
// stated one applies to a unit that states none, a damaged unit gives no
// bitmap, and the bitmap is cropped to its first row with any alpha
// (find_smallest_bounding_rectangle).
//
// The decoder reads the units of a DVD. The units of an HD DVD, with 4-byte
// offsets and 8-bit pixels, give no bitmap.

import (
	"encoding/binary"
	"slices"
)

// FFmpeg joins a unit that is split over packets in a buffer of 64 KiB, and
// discards a unit that would not fit in it.
const largestSubPicture = 0x10000

type vobsubDecoder struct {
	// The height of the canvas. ffprobe reports it as the stream's height,
	// which FFmpeg reads from the size line of the stream's extradata.
	canvasHeight int
	alpha        [4]uint8
	pending      []byte
}

// decode reads one packet and gives the bitmap of the unit that the packet
// completes.
func (d *vobsubDecoder) decode(packet []byte) []imageBitmap {
	unit := packet
	if len(d.pending) > 0 {
		unit = append(d.pending, packet...)
	}
	d.pending = nil
	if len(unit) >= 2 && int(binary.BigEndian.Uint16(unit)) > len(unit) {
		if len(unit) < largestSubPicture {
			d.pending = append([]byte(nil), unit...)
		}
		return nil
	}
	bitmap, shown := d.subPicture(unit)
	if !shown {
		return nil
	}
	return []imageBitmap{bitmap}
}

// finish drops a unit whose end did not arrive before the window ended.
func (d *vobsubDecoder) finish() []imageBitmap {
	d.pending = nil
	return nil
}

// The display area that a control sequence names, and the offsets of its
// two fields, or -1 where the sequence names none.
type spuArea struct {
	x1, y1, x2, y2 int
	top, bottom    int
}

// subPicture reads the control sequences of one unit in their order. Each
// sequence that names an area and both fields decodes the picture again,
// and the last one decoded is the bitmap.
func (d *vobsubDecoder) subPicture(unit []byte) (imageBitmap, bool) {
	if len(unit) < 10 || binary.BigEndian.Uint16(unit) == 0 {
		return imageBitmap{}, false
	}
	var rows []bitmapRow
	var top int
	at := int(binary.BigEndian.Uint16(unit[2:4]))
	for at > 0 && at < len(unit)-4 {
		next := int(binary.BigEndian.Uint16(unit[at+2 : at+4]))
		area, read := d.controlSequence(unit, at+4)
		if !read || area.top >= len(unit) || area.bottom >= len(unit) {
			return imageBitmap{}, false
		}
		width, height := area.x2-area.x1+1, area.y2-area.y1+1
		if area.top >= 0 && area.bottom >= 0 && width > 0 && height > 1 {
			rows = make([]bitmapRow, height)
			if !d.field(unit, area.top, width, rows, 0) || !d.field(unit, area.bottom, width, rows, 1) {
				return imageBitmap{}, false
			}
			top = area.y1
		}
		if next <= at {
			break
		}
		at = next
	}
	if !slices.ContainsFunc(rows, func(row bitmapRow) bool { return row.alpha > 0 }) {
		return imageBitmap{}, false
	}
	return bitmapOf(d.canvasHeight, top, rows, true), true
}

// controlSequence reads the commands of one sequence, from at to the end
// command. It is false when a command is cut short, which FFmpeg reads as
// a damaged unit. A command that FFmpeg does not know ends the sequence.
func (d *vobsubDecoder) controlSequence(unit []byte, at int) (spuArea, bool) {
	area := spuArea{top: -1, bottom: -1}
	for at < len(unit) {
		command := unit[at]
		at++
		rest := unit[at:]
		switch command {
		case 0x00, 0x01, 0x02:
		case 0x03:
			if len(rest) < 2 {
				return area, false
			}
			at += 2
		case 0x04:
			if len(rest) < 2 {
				return area, false
			}
			d.alpha = [4]uint8{rest[1] & 0x0f, rest[1] >> 4, rest[0] & 0x0f, rest[0] >> 4}
			at += 2
		case 0x05:
			if len(rest) < 6 {
				return area, false
			}
			area.x1 = int(rest[0])<<4 | int(rest[1])>>4
			area.x2 = int(rest[1]&0x0f)<<8 | int(rest[2])
			area.y1 = int(rest[3])<<4 | int(rest[4])>>4
			area.y2 = int(rest[4]&0x0f)<<8 | int(rest[5])
			at += 6
		case 0x06:
			if len(rest) < 4 {
				return area, false
			}
			area.top = int(binary.BigEndian.Uint16(rest[0:2]))
			area.bottom = int(binary.BigEndian.Uint16(rest[2:4]))
			at += 4
		default:
			return area, true
		}
	}
	return area, true
}

// field decodes one field of the picture into its rows: the even rows for
// field 0 and the odd rows for field 1. Each
// row starts on a byte. A run is one to four nibbles: the length shifted
// left by two, and the pixel code in the low two bits. A run of length zero
// fills the rest of the row. A run past the end of the row, or code that
// ends before the field does, is a damaged unit.
func (d *vobsubDecoder) field(unit []byte, start, width int, rows []bitmapRow, first int) bool {
	reader := nibbleReader{data: unit, at: start * 2}
	for y := first; y < len(rows); y += 2 {
		for x := 0; x < width; {
			if reader.at > len(unit)*2 {
				return false
			}
			v := 0
			for limit := 1; v < limit && limit <= 0x40; limit <<= 2 {
				v = v<<4 | reader.next()
			}
			code, run := v&3, v>>2
			switch {
			case run == 0:
				run = width - x
			case run > width-x:
				return false
			}
			rows[y].paint(x, run, d.alpha[code]*17)
			x += run
		}
		reader.align()
	}
	return true
}

// A reader of the nibbles of a unit, high nibble first, that reads zero past
// the end the way FFmpeg's bit reader does. at counts nibbles.
type nibbleReader struct {
	data []byte
	at   int
}

func (r *nibbleReader) next() int {
	at := r.at
	r.at++
	if at/2 >= len(r.data) {
		return 0
	}
	if at%2 == 0 {
		return int(r.data[at/2] >> 4)
	}
	return int(r.data[at/2] & 0x0f)
}

func (r *nibbleReader) align() {
	r.at += r.at % 2
}
