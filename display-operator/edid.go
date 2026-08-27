package main

// Reading a monitor's EDID.
//
// EDID is the block of bytes a monitor answers with over the display
// cable's I2C wire. The kernel reads it when a connector detects a
// monitor and exposes it at
// /sys/class/drm/<card>-<connector>/edid, so this operator reads a
// file rather than a bus, and it needs no privilege to read what is
// plugged in.
//
// The format is VESA's, and the part this operator reads is the first
// 128-byte block. Byte 0 to byte 7 are a fixed header. Bytes 8 and 9
// hold the manufacturer's PNP id as three five-bit letters. Bytes 10
// and 11 hold the product code. Bytes 54 to 125 hold four 18-byte
// descriptors: a descriptor whose first two bytes are zero holds
// text or timing limits, and any other descriptor is a detailed
// timing.
//
// The first detailed timing is the preferred mode, which is the mode
// the operator writes into weston.ini as mode=preferred, so the
// published pixel size is the size the compositor drives.
//
// One exception qualifies that: a connector whose claim states a
// mode runs that mode instead. weston.ini names it, and the
// currentMode attribute says which mode the output runs now.
//
// A monitor may state its physical size twice. Bytes 21 and 22 give
// it in whole centimeters, and the detailed timing gives it in
// millimeters. The millimeters win where they exist: the portable
// monitor on the lab machine reports 29 by 27 centimeters in bytes 21
// and 22, and 344 by 196 millimeters in its timing. Only the
// second one describes a 15.6-inch panel.

import (
	"errors"
	"fmt"
	"strings"
)

// blockSize is the length of one EDID block. A monitor with extension
// blocks answers with several of them, and everything this operator
// publishes is in the first one.
const blockSize = 128

// descriptor tags. The tag is byte 3 of a descriptor whose first
// three bytes are zero.
const (
	tagSerialNumber = 0xff
	tagMonitorName  = 0xfc
)

var edidHeader = []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// ErrNoEDID reports a connector that answered with nothing. The
// kernel leaves the file empty while no monitor is attached, and a
// read of it succeeds, so the empty answer is what says the monitor
// is gone.
var ErrNoEDID = errors.New("the connector reports no EDID")

// EDID holds the facts this operator publishes about one monitor.
type EDID struct {
	// Manufacturer is the three-letter PNP id, uppercase, as the
	// registry spells it: GSM is LG Electronics, BOE is BOE
	// Technology.
	Manufacturer string
	// ProductCode is the model number the manufacturer assigns. It is
	// a number, and every tool that prints it prints it as four
	// hexadecimal digits.
	ProductCode uint16
	// Serial is the serial number descriptor's text, or the base
	// block's 32-bit serial in decimal when the monitor writes no
	// descriptor, or empty when it states neither.
	Serial string
	// ModelName is the monitor name descriptor's text. A panel that
	// was built into a laptop often has none, because nobody reads a
	// model name off a screen with no bezel button.
	ModelName string
	// WidthPixels and HeightPixels are the preferred mode's active
	// area.
	WidthPixels  int
	HeightPixels int
	// RefreshMillihertz is the preferred mode's refresh rate, in
	// millihertz, or zero when the timing states no raster. See
	// refreshMillihertz for why the unit is not hertz.
	RefreshMillihertz int
	// WidthMillimeters and HeightMillimeters are the panel's physical
	// size.
	WidthMillimeters  int
	HeightMillimeters int
	// HDMIInput is the number of the sink's own port that this
	// cable occupies, read from the CEC physical address in the HDMI
	// vendor block. An HDMI sink serves each of its ports an EDID
	// carrying that port's address, so this is the one channel where
	// the panel names which input is ours. It is zero when the monitor
	// serves no address, serves the degenerate one, or serves an
	// address deeper than a single port.
	HDMIInput int
}

// ParseEDID reads the facts this operator publishes out of one
// monitor's EDID.
//
// The checksum is a hard test. Every byte of the first block sums to
// zero modulo 256, and a block that fails it was read off a noisy
// wire, so publishing its manufacturer and its serial would name the
// wrong monitor.
func ParseEDID(raw []byte) (EDID, error) {
	if len(raw) == 0 {
		return EDID{}, ErrNoEDID
	}
	if len(raw) < blockSize {
		return EDID{}, fmt.Errorf("an EDID block is %d bytes, this one is %d", blockSize, len(raw))
	}
	block := raw[:blockSize]
	if string(block[:8]) != string(edidHeader) {
		return EDID{}, errors.New("the EDID header is missing")
	}
	var sum byte
	for _, b := range block {
		sum += b
	}
	if sum != 0 {
		return EDID{}, errors.New("the EDID checksum does not add up")
	}

	edid := EDID{
		Manufacturer: manufacturerID(block[8], block[9]),
		ProductCode:  uint16(block[10]) | uint16(block[11])<<8,
		// Bytes 21 and 22 are whole centimeters, which is the coarse
		// answer. A detailed timing below overwrites it in
		// millimeters.
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
			// mode, and the physical size is in the same descriptor.
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

// refreshMillihertz computes the preferred mode's refresh rate from
// its detailed timing. The rate is not a field of the descriptor: a
// monitor states a pixel clock and a raster, and the refresh is the
// clock divided by the whole raster, blanking included. The unit is
// millihertz because real modes land near a round number and not on
// it. This repository's own fixtures run at 59.999 and 59.998 Hz,
// and an attribute in whole hertz would erase the difference.
//
// A zero clock or a zero total is a descriptor that states no mode,
// so the answer is zero and the caller publishes nothing.
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

// readDescriptor takes the text out of one display descriptor. A
// descriptor whose tag this operator does not publish is left alone.
func (edid *EDID) readDescriptor(descriptor []byte) {
	switch descriptor[3] {
	case tagMonitorName:
		// Only a name that says something replaces one that is
		// already there. A monitor may write the descriptor twice,
		// once in the base block and once in a CTA extension, and the
		// second one is sometimes 13 spaces. An empty name would drop
		// the model attribute and shorten monitor.liken.sh/id, which
		// the audio operator matches byte for byte.
		if text := descriptorText(descriptor); text != "" {
			edid.ModelName = text
		}
	case tagSerialNumber:
		// The text serial wins over the number. It is what the label
		// on the back of the monitor reads, and it is what a person
		// writes into a claim that names one unit.
		if text := descriptorText(descriptor); text != "" {
			edid.Serial = text
		}
	}
}

// readExtensions reads the display descriptors in the CTA-861
// extension blocks.
//
// A monitor with a CTA extension often writes its name and its serial
// there rather than in the base block, and the LG ultrawide on the lab
// machine is one: its base block states the numeric serial 420070 and
// its extension states the string 202NTRLCC070, which is the number
// printed on the back of the monitor.
//
// Only the descriptors are read. The extension's own detailed timings
// are alternative modes, and the preferred mode is the base block's
// first timing, which is what the compositor drives.
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
		// Tag 0x02 is CTA-861. A revision below 3 has no descriptor
		// area, and byte 2 holds the offset where the descriptors
		// start. An offset of 0 says the block holds none.
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
		if input := hdmiInput(extension); input != 0 {
			edid.HDMIInput = input
		}
	}
}

// The CTA-861 data block collection sits between the header
// and the descriptors, one block after another. Each block starts with
// a byte carrying the block's tag in its top three bits and the length
// of what follows in its low five, so the walk needs no other table to
// step through blocks it does not read.
const (
	ceaBlocksStart = 4
	ceaVendorTag   = 3
)

// The HDMI Licensing OUI, 00-0C-03, as the block stores it:
// least significant byte first.
var hdmiVendorOUI = [3]byte{0x03, 0x0c, 0x00}

// hdmiInput walks the data blocks for the HDMI vendor block and
// reads the port out of the physical address behind the OUI. A block
// with another OUI, another tag, or too few bytes to carry an address
// states nothing about this cable.
func hdmiInput(extension []byte) int {
	end := int(extension[2])
	if end > blockSize {
		return 0
	}
	for offset := ceaBlocksStart; offset < end; {
		header := extension[offset]
		tag, length := int(header>>5), int(header&0x1f)
		payload := offset + 1
		if payload+length > end {
			return 0
		}
		if tag == ceaVendorTag && length >= 5 &&
			[3]byte(extension[payload:payload+3]) == hdmiVendorOUI {
			return physicalAddressPort(extension[payload+3], extension[payload+4])
		}
		offset = payload + length
	}
	return 0
}

// The physical address is four nibbles, A.B.C.D, packed into
// two bytes. Only the form N.0.0.0 names a port of this sink: 0.0.0.0
// is what a sink serves when it states none, and anything deeper names
// a device behind a repeater rather than the port this cable is in.
func physicalAddressPort(high, low byte) int {
	if low != 0x00 || high&0x0f != 0x00 {
		return 0
	}
	return int(high >> 4)
}

// manufacturerID decodes the PNP id. The two bytes are big-endian,
// and they hold three five-bit letters where 1 is A. The top bit is
// reserved and this decoder ignores it, because a monitor that sets it
// still names a real manufacturer in the other fifteen.
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

// descriptorText reads the 13 bytes of text a display descriptor
// holds. The text ends at a line feed and pads with spaces to the
// end of the field, so both have to come off before the value is
// published.
func descriptorText(descriptor []byte) string {
	text := string(descriptor[5:18])
	if end := strings.IndexByte(text, '\n'); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text)
}
