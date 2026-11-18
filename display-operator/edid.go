package main

// Parsing a monitor's EDID.
//
// EDID is the block of bytes that a monitor returns over the display
// cable's I2C lines. The kernel reads it when a connector detects a
// monitor and writes it to /sys/class/drm/<card>-<connector>/edid.
// This operator reads that file, so it needs no privilege and no bus
// access.
//
// Most of what the operator publishes is in the first 128-byte block
// (VESA's format). Bytes 0 to 7 are a fixed header. Bytes 8 and 9 hold
// the manufacturer's PNP id as three five-bit letters, and bytes 10
// and 11 hold the product code. Bytes 54 to 125 hold four 18-byte
// descriptors. A descriptor that starts with two zero bytes holds text
// or timing limits. Any other descriptor is a detailed timing.
//
// The first detailed timing is the monitor's preferred mode. weston
// drives that mode for mode=preferred, which weston.ini gets for every
// connector whose claim or Display sets no mode. The currentMode
// attribute reports the mode that the output actually runs.
//
// A monitor can report its physical size twice: in whole centimeters
// in bytes 21 and 22, and in millimeters in the detailed timing. The
// parser takes the millimeters when the timing has them, because the
// centimeters are coarse and can be wrong. The portable-display
// fixture reports 29 by 27 centimeters and 344 by 196 millimeters, and
// only the millimeters match its 15.6-inch panel.

import (
	"errors"
	"fmt"
	"strings"
)

// blockSize is the length of one EDID block. A monitor with extension
// blocks returns the base block followed by each extension.
const blockSize = 128

// Display descriptor tags. The tag is byte 3 of a descriptor that
// starts with zero bytes.
const (
	tagSerialNumber = 0xff
	tagMonitorName  = 0xfc
)

var edidHeader = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// ErrNoEDID means the connector returned no EDID. The kernel leaves the
// file empty while no monitor is attached, and reading it still
// succeeds, so an empty file is the only sign that the monitor is gone.
var ErrNoEDID = errors.New("the connector has no EDID, so no monitor is attached")

// EDID holds the fields of one monitor that the operator publishes.
type EDID struct {
	// Manufacturer is the three-letter uppercase PNP id, for example
	// GSM for LG Electronics or BOE for BOE Technology.
	Manufacturer string
	// ProductCode is the manufacturer's model number. Tools print it as
	// four hexadecimal digits.
	ProductCode uint16
	// Serial is the text of the serial number descriptor. When the
	// monitor has no such descriptor, it is the base block's 32-bit
	// serial in decimal. It is empty when the monitor has neither.
	Serial string
	// ModelName is the text of the monitor name descriptor. A laptop's
	// built-in panel often has none.
	ModelName string
	// WidthPixels and HeightPixels are the active area of the preferred
	// mode.
	WidthPixels  int
	HeightPixels int
	// RefreshMillihertz is the refresh rate of the preferred mode, or
	// zero when the timing has no pixel clock or no raster size. See
	// refreshMillihertz for why the unit is millihertz.
	RefreshMillihertz int
	// WidthMillimeters and HeightMillimeters are the panel's physical
	// size.
	WidthMillimeters  int
	HeightMillimeters int
	// PhysicalAddress is the CEC physical address of the sink port that
	// this cable is plugged into, in dotted form such as 1.2.0.0. The
	// HDMI vendor block supplies it. It is empty when the monitor has no
	// HDMI vendor block or the address is not valid. physicaladdress.go
	// has the rules.
	PhysicalAddress string
}

// ParseEDID parses one monitor's EDID. It returns ErrNoEDID for an
// empty file. It fails when the base block is short, has a wrong
// header, or has bytes that do not sum to zero modulo 256. A block that
// fails the checksum may have been corrupted on the wire, and
// publishing its manufacturer and serial could identify the wrong
// monitor.
func ParseEDID(raw []byte) (EDID, error) {
	if len(raw) == 0 {
		return EDID{}, ErrNoEDID
	}
	if len(raw) < blockSize {
		return EDID{}, fmt.Errorf("the EDID holds %d bytes, and a base block is %d bytes", len(raw), blockSize)
	}
	block := raw[:blockSize]
	if string(block[:8]) != string(edidHeader) {
		return EDID{}, errors.New("the EDID does not start with the header 00 ff ff ff ff ff ff 00")
	}
	var sum byte
	for _, b := range block {
		sum += b
	}
	if sum != 0 {
		return EDID{}, errors.New("the bytes of the EDID base block do not sum to 0 modulo 256")
	}

	edid := EDID{
		Manufacturer: manufacturerID(block[8], block[9]),
		ProductCode:  uint16(block[10]) | uint16(block[11])<<8,
		// Whole centimeters, until the detailed timing below
		// replaces them with millimeters.
		WidthMillimeters:  int(block[21]) * 10,
		HeightMillimeters: int(block[22]) * 10,
	}

	numericSerial := uint32(block[12]) | uint32(block[13])<<8 | uint32(block[14])<<16 | uint32(block[15])<<24
	if numericSerial != 0 {
		edid.Serial = fmt.Sprintf("%d", numericSerial)
	}

	preferred := true
	for offset := 54; offset+18 <= 126; offset += 18 {
		descriptor := block[offset : offset+18]
		if descriptor[0] != 0 || descriptor[1] != 0 {
			// A detailed timing. Only the first one is the preferred
			// mode, and the physical size is read from it too.
			if !preferred {
				continue
			}
			preferred = false
			edid.WidthPixels = int(descriptor[2]) | int(descriptor[4]>>4)<<8
			edid.HeightPixels = int(descriptor[5]) | int(descriptor[7]>>4)<<8
			edid.RefreshMillihertz = refreshMillihertz(descriptor)
			width := int(descriptor[12]) | int(descriptor[14]>>4)<<8
			height := int(descriptor[13]) | int(descriptor[14]&0x0f)<<8
			if width > 0 && height > 0 {
				edid.WidthMillimeters, edid.HeightMillimeters = width, height
			}
			continue
		}
		edid.readDescriptor(descriptor)
	}
	edid.readExtensions(raw, int(block[126]))
	return edid, nil
}

// refreshMillihertz computes the refresh rate of a detailed timing.
// The descriptor has no refresh field. It has a pixel clock and the
// raster size including blanking, and the rate is the clock divided by
// the raster. The unit is millihertz because real modes run close to a
// round rate without matching it, and whole hertz would hide the
// difference: the fixtures in testdata run at 59.999 Hz and 59.998 Hz.
// It returns zero when the clock or either raster total is zero.
func refreshMillihertz(descriptor []byte) int {
	pixelClockHz := (int(descriptor[0]) | int(descriptor[1])<<8) * 10_000
	horizontalTotal := int(descriptor[2]) | int(descriptor[4]>>4)<<8
	horizontalTotal += int(descriptor[3]) | int(descriptor[4]&0x0f)<<8
	verticalTotal := int(descriptor[5]) | int(descriptor[7]>>4)<<8
	verticalTotal += int(descriptor[6]) | int(descriptor[7]&0x0f)<<8
	if pixelClockHz == 0 || horizontalTotal == 0 || verticalTotal == 0 {
		return 0
	}
	return pixelClockHz * 1_000 / (horizontalTotal * verticalTotal)
}

// readDescriptor reads the model name or the serial number from a
// display descriptor. It ignores any other tag.
func (edid *EDID) readDescriptor(descriptor []byte) {
	switch descriptor[3] {
	case tagMonitorName:
		// A blank name does not replace an earlier one. Some monitors
		// write this descriptor in the base block and again in a CTA
		// extension, with 13 spaces in the second. An empty name would
		// drop the model attribute and shorten monitor.liken.sh/id.
		// audio-operator pairs a screen with its speakers by that id,
		// so the pairing would fail with no error to explain it.
		if text := descriptorText(descriptor); text != "" {
			edid.ModelName = text
		}
	case tagSerialNumber:
		// The text serial replaces the numeric one, because it matches
		// the serial on the monitor's label, which is what a person
		// writes in a claim to select one unit.
		if text := descriptorText(descriptor); text != "" {
			edid.Serial = text
		}
	}
}

// readExtensions reads the display descriptors and the HDMI vendor
// block from the CTA-861 extension blocks that follow the base block.
// A monitor can put its name or its text serial in an extension
// instead of the base block, as the lg-hdr-wqhd fixture does with its
// serial. The first extension with a valid physical address sets
// PhysicalAddress.
//
// It skips the extension's detailed timings, because they are
// alternative modes and the preferred mode is in the base block.
func (edid *EDID) readExtensions(raw []byte, count int) {
	for block := 1; block <= count; block++ {
		start := block * blockSize
		if start+blockSize > len(raw) {
			return
		}
		extension := raw[start : start+blockSize]
		var sum byte
		for _, b := range extension {
			sum += b
		}
		// Read only a CTA-861 extension (tag 0x02) of revision 3 or
		// later with a valid checksum. Byte 2 is the offset where the
		// detailed timings and descriptors start. 0 means the block has
		// none, and 1 to 3 are not valid offsets.
		if sum != 0 || extension[0] != 0x02 || extension[1] < 3 || extension[2] < 4 {
			continue
		}
		for offset := int(extension[2]); offset+18 <= blockSize-1; offset += 18 {
			descriptor := extension[offset : offset+18]
			if descriptor[0] != 0 || descriptor[1] != 0 {
				continue
			}
			edid.readDescriptor(descriptor)
		}
		if edid.PhysicalAddress == "" {
			edid.PhysicalAddress = physicalAddress(extension)
		}
	}
}

// manufacturerID decodes the PNP id from two big-endian bytes that hold
// three five-bit letters, where 1 is A. It ignores the top bit, which
// is reserved, because a monitor that sets it still has a valid id in
// the other 15 bits. It returns an empty string when a letter is out
// of range.
func manufacturerID(high, low byte) string {
	packed := uint16(high)<<8 | uint16(low)
	letters := []byte{
		byte((packed>>10)&0x1f) + 'A' - 1,
		byte((packed>>5)&0x1f) + 'A' - 1,
		byte(packed&0x1f) + 'A' - 1,
	}
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return ""
		}
	}
	return string(letters)
}

// descriptorText returns the 13 bytes of text in a display descriptor.
// The text ends at a line feed and the rest of the field is padded
// with spaces, so it drops both.
func descriptorText(descriptor []byte) string {
	text := string(descriptor[5:18])
	if end := strings.IndexByte(text, '\n'); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text)
}
