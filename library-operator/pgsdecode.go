package main

// pgsdecode.go decodes the PGS subtitle stream of a Blu-ray into the
// bitmaps that mpv receives, for the measurement in imagemeasure.go. A PGS
// packet is one display set of segments: a presentation composition that
// places objects on the canvas, palettes, the run-length code of each
// object, and an end segment that shows the composition. FFmpeg's
// libavcodec/pgssubdec.c decodes the same segments for mpv, and this
// decoder follows it where the two could differ: a composition state other
// than normal releases every palette and object, and the cropping that a
// composition can state is not applied, because FFmpeg does not apply it.
//
// The decoder keeps only the alpha of each palette entry, because the
// measurement reads only alpha, and it keeps the code of the objects of
// the current epoch and of the cue on screen. So its memory does not grow
// with the number of cues.

import (
	"bytes"
	"encoding/binary"
)

// The segment types of a display set.
const (
	pgsPalette      = 0x14
	pgsObjectCode   = 0x15
	pgsComposition  = 0x16
	pgsEndOfDisplay = 0x80
)

type pgsDecoder struct {
	palettes    map[uint8]*[256]uint8
	objects     map[uint16]*pgsObject
	composition *pgsCompositionSegment
	shown       *pgsCue
}

// One object of the epoch: its size, and its run-length code, which can
// arrive in several segments until remaining is zero.
type pgsObject struct {
	width     int
	height    int
	rle       []byte
	remaining int
}

// One object that a composition places on the canvas.
type pgsPlacement struct {
	id   uint16
	x, y int
}

type pgsCompositionSegment struct {
	canvasHeight int
	palette      uint8
	placements   []pgsPlacement
}

// The cue on screen: the objects that its composition placed, and the most
// opaque palette that it was shown with.
type pgsCue struct {
	canvasHeight int
	placements   []pgsPlacement
	objects      []*pgsObject
	alpha        [256]uint8
	opacity      uint8
}

// decode reads one packet and gives the bitmaps of the cue that the packet
// took off the screen.
func (d *pgsDecoder) decode(packet []byte) []imageBitmap {
	var done []imageBitmap
	for len(packet) >= 3 {
		kind, length := packet[0], int(binary.BigEndian.Uint16(packet[1:3]))
		if len(packet) < 3+length {
			break
		}
		body := packet[3 : 3+length]
		packet = packet[3+length:]
		switch kind {
		case pgsComposition:
			d.compose(body)
		case pgsPalette:
			d.paint(body)
		case pgsObjectCode:
			d.define(body)
		case pgsEndOfDisplay:
			done = append(done, d.show()...)
		}
	}
	return done
}

// finish gives the bitmaps of the cue still on screen when the window ends,
// and forgets the epoch, because the next packet the decoder reads can be
// minutes of the title away.
func (d *pgsDecoder) finish() []imageBitmap {
	done := d.release()
	*d = pgsDecoder{}
	return done
}

// A composition names the canvas, the palette, and the objects of the next
// cue. Any state but normal starts an epoch or is a point where a player can
// start to read, and FFmpeg releases every palette and object there.
func (d *pgsDecoder) compose(body []byte) {
	if len(body) < 11 {
		return
	}
	if body[7]>>6 != 0 {
		d.palettes, d.objects = nil, nil
	}
	composition := &pgsCompositionSegment{
		canvasHeight: int(binary.BigEndian.Uint16(body[2:4])),
		palette:      body[9],
	}
	placed := body[11:]
	for range int(body[10]) {
		if len(placed) < 8 {
			break
		}
		composition.placements = append(composition.placements, pgsPlacement{
			id: binary.BigEndian.Uint16(placed[0:2]),
			x:  int(binary.BigEndian.Uint16(placed[4:6])),
			y:  int(binary.BigEndian.Uint16(placed[6:8])),
		})
		cropped := placed[3]&0x80 != 0
		placed = placed[8:]
		if cropped && len(placed) >= 8 {
			placed = placed[8:]
		}
	}
	d.composition = composition
}

// A palette segment sets some entries of one palette. Each entry is five
// bytes: the entry, Y, Cr, Cb, and alpha.
func (d *pgsDecoder) paint(body []byte) {
	if len(body) < 2 {
		return
	}
	if d.palettes == nil {
		d.palettes = map[uint8]*[256]uint8{}
	}
	palette := d.palettes[body[0]]
	if palette == nil {
		palette = &[256]uint8{}
		d.palettes[body[0]] = palette
	}
	for entries := body[2:]; len(entries) >= 5; entries = entries[5:] {
		palette[entries[0]] = entries[4]
	}
}

// An object segment starts an object's code or continues it. The length in
// the first segment counts the four bytes of the width and the height.
func (d *pgsDecoder) define(body []byte) {
	if len(body) < 4 {
		return
	}
	id := binary.BigEndian.Uint16(body[0:2])
	if body[3]&0x80 == 0 {
		object := d.objects[id]
		if object == nil || len(body)-4 > object.remaining {
			return
		}
		object.rle = append(object.rle, body[4:]...)
		object.remaining -= len(body) - 4
		return
	}
	if len(body) < 11 {
		return
	}
	length := int(body[4])<<16 | int(body[5])<<8 | int(body[6]) - 4
	object := &pgsObject{
		width:  int(binary.BigEndian.Uint16(body[7:9])),
		height: int(binary.BigEndian.Uint16(body[9:11])),
		rle:    bytes.Clone(body[11:]),
	}
	object.remaining = length - len(object.rle)
	if object.width == 0 || object.height == 0 || object.remaining < 0 {
		return
	}
	if d.objects == nil {
		d.objects = map[uint16]*pgsObject{}
	}
	d.objects[id] = object
}

// The end segment shows the composition. When it shows the same objects as
// the cue on screen, it is the same cue: a fade sends a palette that is more
// opaque or less, and a point where a player can start to read sends the
// objects again. The cue keeps its most opaque palette. Any other
// composition takes the cue off the screen, and puts a new one on it when
// every object it names is defined.
func (d *pgsDecoder) show() []imageBitmap {
	composition := d.composition
	d.composition = nil
	if composition == nil {
		return nil
	}
	objects := make([]*pgsObject, 0, len(composition.placements))
	for _, placement := range composition.placements {
		object := d.objects[placement.id]
		if object == nil || object.remaining > 0 {
			objects = nil
			break
		}
		objects = append(objects, object)
	}
	var alpha [256]uint8
	if palette := d.palettes[composition.palette]; palette != nil {
		alpha = *palette
	}
	if d.shown.isShowing(composition.placements, objects) {
		if opacity := maxAlpha(alpha); opacity > d.shown.opacity {
			d.shown.alpha, d.shown.opacity = alpha, opacity
		}
		return nil
	}
	done := d.release()
	if len(objects) > 0 {
		d.shown = &pgsCue{
			canvasHeight: composition.canvasHeight,
			placements:   composition.placements,
			objects:      objects,
			alpha:        alpha,
			opacity:      maxAlpha(alpha),
		}
	}
	return done
}

func (c *pgsCue) isShowing(placements []pgsPlacement, objects []*pgsObject) bool {
	if c == nil || len(objects) == 0 || len(objects) != len(c.objects) {
		return false
	}
	for i, object := range objects {
		if placements[i] != c.placements[i] || !bytes.Equal(object.rle, c.objects[i].rle) {
			return false
		}
	}
	return true
}

func maxAlpha(alpha [256]uint8) uint8 {
	var most uint8
	for _, a := range alpha {
		most = max(most, a)
	}
	return most
}

// release takes the cue off the screen and gives one bitmap for each of its
// objects, at the place its composition gave it.
func (d *pgsDecoder) release() []imageBitmap {
	cue := d.shown
	d.shown = nil
	if cue == nil {
		return nil
	}
	bitmaps := make([]imageBitmap, len(cue.objects))
	for i, object := range cue.objects {
		bitmaps[i] = bitmapOf(cue.canvasHeight, cue.placements[i].y, object.rows(&cue.alpha), false)
	}
	return bitmaps
}

// rows decodes the object's code into the largest alpha of each of its
// rows. A byte that is not zero is one pixel of that entry. A zero byte is
// followed by a flag byte: zero ends the line, bit 0x40 makes the run's
// length 14 bits long, and bit 0x80 names an entry, where entry 0 is
// otherwise meant. A pixel past the width or a line past the height is
// dropped.
func (o *pgsObject) rows(alpha *[256]uint8) []uint8 {
	rows := make([]uint8, o.height)
	x, y := 0, 0
	code := o.rle
	for len(code) > 0 && y < o.height {
		entry, run := code[0], 1
		code = code[1:]
		if entry == 0 {
			if len(code) == 0 {
				break
			}
			flags := code[0]
			code = code[1:]
			if flags == 0 {
				x, y = 0, y+1
				continue
			}
			run = int(flags & 0x3f)
			if flags&0x40 != 0 && len(code) > 0 {
				run = run<<8 | int(code[0])
				code = code[1:]
			}
			if flags&0x80 != 0 && len(code) > 0 {
				entry = code[0]
				code = code[1:]
			}
		}
		if run > 0 && x < o.width {
			rows[y] = max(rows[y], alpha[entry])
		}
		x += run
	}
	return rows
}
