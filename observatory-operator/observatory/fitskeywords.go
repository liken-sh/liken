package observatory

// Declared FITS keywords (plan 20). A frame's header holds two kinds of
// keyword. The camera's driver measures some at capture, such as
// EXPTIME and DATE-OBS, and a person declares the others, such as
// OBSERVER, which do not change from frame to frame. The Observatory,
// the Telescope, the Camera, and the Reservation can each declare
// keywords in spec.fitsKeywords, and the operator merges the four
// lists and writes the result to the camera's FITS_HEADER, so the
// driver writes every keyword when it creates the file.
//
// The driver does not protect what it measures: a written EXPTIME
// replaces the exposure's real length in every later frame. A header
// that lies is worse than a header that lacks a keyword, so the CRDs
// refuse a declared keyword that the driver writes. schema/ writes
// DriverFITSKeywords into that rule, so this file is its one source.

import "slices"

// FITSKeyword is one keyword that a person declares for the header of
// every frame. The driver writes a value that reads as a number as a
// number, and any other value as a quoted string.
type FITSKeyword struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// DriverFITSKeywords are the keywords that INDI's CCD writes into every
// frame by itself. All but OFFSET, DARKTIME, and MPSAS are in
// indi/testdata/ccd/frame-header.txt, the header of a frame that the
// CCD simulator saved with the mount, focuser, rotator, filter wheel,
// and SQM simulators connected. libindi's CCD writes those three when
// the camera or an SQM reports them, and the simulators did not.
var DriverFITSKeywords = []string{
	// The structure of the file.
	"SIMPLE", "BITPIX", "NAXIS", "NAXIS1", "NAXIS2", "EXTEND", "BZERO",
	"BSCALE", "ROWORDER",
	// The names of the INDI devices.
	"INSTRUME", "TELESCOP",
	// The measures of the exposure.
	"EXPTIME", "CCD-TEMP", "PIXSIZE1", "PIXSIZE2", "XBINNING", "YBINNING",
	"XPIXSZ", "YPIXSZ", "FRAME", "IMAGETYP", "FILTER", "FOCALLEN",
	"APTDIA", "ROTATANG", "FOCUSPOS", "FOCUSTEM", "SCALE", "SITELAT",
	"SITELONG", "AIRMASS", "OBJCTAZ", "OBJCTALT", "OBJCTRA", "OBJCTDEC",
	"RA", "DEC", "PIERSIDE", "EQUINOX", "DATE-OBS", "GAIN", "OFFSET",
	"DARKTIME", "MPSAS",
}

// renamedFITSKeywords are the keywords of DriverFITSKeywords that a
// person can declare. The driver fills INSTRUME and TELESCOP with the
// names of INDI devices, such as "CCD Simulator", which name a driver
// and not the person's equipment.
var renamedFITSKeywords = []string{"INSTRUME", "TELESCOP"}

// RefusedFITSKeywords answers the names that the CRDs refuse in
// spec.fitsKeywords: each keyword that the driver measures, and
// COMMENT, HISTORY, and END, which FITS reserves for cards that hold
// no value.
func RefusedFITSKeywords() []string {
	refused := slices.DeleteFunc(slices.Clone(DriverFITSKeywords), func(name string) bool {
		return slices.Contains(renamedFITSKeywords, name)
	})
	return append(refused, "COMMENT", "HISTORY", "END")
}

// MergeFITSKeywords merges the lists of the levels, from the least
// specific to the most: the Observatory, the Telescope, the Camera, and
// the Reservation. A keyword at a later level replaces the value and
// the comment of the same name at an earlier one, and keeps that
// name's place, so the header lists each name where it first appears.
func MergeFITSKeywords(levels ...[]FITSKeyword) []FITSKeyword {
	var merged []FITSKeyword
	for _, level := range levels {
		for _, k := range level {
			if i := slices.IndexFunc(merged, func(m FITSKeyword) bool { return m.Name == k.Name }); i >= 0 {
				merged[i] = k
				continue
			}
			merged = append(merged, k)
		}
	}
	return merged
}
