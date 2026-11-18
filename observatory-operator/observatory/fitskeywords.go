package observatory

// The camera's driver writes the keywords that it measures at capture,
// such as EXPTIME and DATE-OBS, into each frame's FITS header. A person
// declares the keywords whose values stay the same from frame to frame,
// such as OBSERVER, in spec.fitsKeywords of the Observatory, the
// Telescope, the Camera, or the Reservation. The operator merges those
// lists and writes the result to the camera's FITS_HEADER property, and
// the driver adds them to each file it creates.
//
// The driver does not check a declared keyword against its own
// measurements, so a declared EXPTIME would replace the real exposure
// length in every later frame. A wrong value is worse than a missing
// keyword, so the CRDs refuse a declared keyword that the driver
// writes. schema/ generates that rule from DriverFITSKeywords.

import "slices"

// FITSKeyword is one keyword that a person declares for the header of
// every frame. The driver writes a value that parses as a number as a
// number, and any other value as a quoted string.
type FITSKeyword struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// DriverFITSKeywords are the keywords that INDI's CCD driver writes
// into a frame on its own. The keywords from SIMPLE to GAIN are in
// indi/inditest/testdata/ccd/frame-header.txt, the header of a frame
// that the CCD simulator saved with the mount, focuser, rotator, filter
// wheel, and SQM simulators connected. libindi's CCD class
// (libs/indibase/indiccd.cpp) writes the others only in conditions that
// the capture did not meet: OFFSET, DARKTIME, and MPSAS when the camera
// or an SQM reports them, the Bayer keywords for a color sensor,
// DATAMIN and DATAMAX for some frames, and the WCS keywords when it
// writes a world coordinate system.
var DriverFITSKeywords = []string{
	// The structure of the file.
	"SIMPLE", "BITPIX", "NAXIS", "NAXIS1", "NAXIS2", "EXTEND", "BZERO",
	"BSCALE", "ROWORDER",
	// The names of the INDI devices.
	"INSTRUME", "TELESCOP",
	// The measurements of the exposure.
	"EXPTIME", "CCD-TEMP", "PIXSIZE1", "PIXSIZE2", "XBINNING", "YBINNING",
	"XPIXSZ", "YPIXSZ", "FRAME", "IMAGETYP", "FILTER", "FOCALLEN",
	"APTDIA", "ROTATANG", "FOCUSPOS", "FOCUSTEM", "SCALE", "SITELAT",
	"SITELONG", "AIRMASS", "OBJCTAZ", "OBJCTALT", "OBJCTRA", "OBJCTDEC",
	"RA", "DEC", "PIERSIDE", "EQUINOX", "DATE-OBS", "GAIN",
	// The measurements that the capture did not have.
	"OFFSET", "DARKTIME", "MPSAS",
	// The color sensor and the range of the pixels.
	"BAYERPAT", "XBAYROFF", "YBAYROFF", "DATAMIN", "DATAMAX",
	// The world coordinate system.
	"CTYPE1", "CTYPE2", "CRVAL1", "CRVAL2", "CRPIX1", "CRPIX2",
	"CDELT1", "CDELT2", "CROTA1", "CROTA2", "SECPIX1", "SECPIX2",
	"RADECSYS",
}

// renamedFITSKeywords are the keywords of DriverFITSKeywords that a
// person can still declare. The driver fills INSTRUME and TELESCOP with
// the names of INDI devices, such as "CCD Simulator", and a person
// declares them to give the names of the real equipment.
var renamedFITSKeywords = []string{"INSTRUME", "TELESCOP"}

// AstrophotographyFITSKeywords are the keywords that
// astrophotography-operator writes with a new value for each frame it
// takes. Its catalog and archive use FRAMEID as the frame's key, and a
// declared FRAMEID would give every frame of a session the same key,
// including the frames that a desktop client takes.
var AstrophotographyFITSKeywords = []string{"FRAMEID"}

// RefusedFITSKeywords returns the names that the CRDs refuse in
// spec.fitsKeywords: each keyword that the driver writes except the
// ones in renamedFITSKeywords, each keyword that
// astrophotography-operator writes, and COMMENT, HISTORY, and END,
// which FITS reserves for cards that have no value.
func RefusedFITSKeywords() []string {
	refused := slices.DeleteFunc(slices.Clone(DriverFITSKeywords), func(name string) bool {
		return slices.Contains(renamedFITSKeywords, name)
	})
	refused = append(refused, AstrophotographyFITSKeywords...)
	return append(refused, "COMMENT", "HISTORY", "END")
}

// MergeFITSKeywords merges keyword lists given from the least specific
// level to the most: the Observatory, the Telescope, the Camera, and the
// Reservation. A later level's keyword replaces the value and comment of
// the same name, at the position where that name first appeared.
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
