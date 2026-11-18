package solver

// Reads a solve's result from the WCS file that astrometry.net writes.
// With --crpix-center, the reference pixel is the center of the frame,
// so CRVAL1 and CRVAL2 are the frame's center. The CD matrix maps
// pixels to degrees on the sky. Its determinant gives the scale and the
// parity, and its terms give the rotation as astrometry.net computes it
// (tan_get_orientation in util/sip.c).

import (
	"math"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// solutionOf builds a SolveResponse from a WCS header.
func solutionOf(h fits.Header) (SolveResponse, error) {
	var values [6]float64
	for i, key := range []string{"CRVAL1", "CRVAL2", "CD1_1", "CD1_2", "CD2_1", "CD2_2"} {
		value, err := h.Float(key)
		if err != nil {
			return SolveResponse{}, err
		}
		values[i] = value
	}
	ra, dec, cd11, cd12, cd21, cd22 := values[0], values[1], values[2], values[3], values[4], values[5]
	det := cd11*cd22 - cd12*cd21
	// A negative determinant means east is to the left of north, as on
	// the sky. astrometry.net calls that parity positive.
	parity, sign := ParityPositive, -1.0
	if det >= 0 {
		parity, sign = ParityNegative, 1.0
	}
	rotation := -math.Atan2(sign*cd21-cd12, sign*cd11+cd22) * 180 / math.Pi
	return SolveResponse{
		Solved:   true,
		Center:   &sky.Position{RA: ra, Dec: dec},
		Scale:    math.Sqrt(math.Abs(det)) * 3600,
		Rotation: math.Mod(rotation+360, 360),
		Parity:   parity,
		Index:    indexOf(h),
	}, nil
}

// indexOf reads the ID of the index series that solved the field from a
// header comment such as "index id: 4207". It returns 0 when the header
// has no such comment.
func indexOf(h fits.Header) int32 {
	for _, comment := range h.Comments() {
		if text, found := strings.CutPrefix(comment, "index id:"); found {
			id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
			if err == nil {
				return int32(id)
			}
		}
	}
	return 0
}
