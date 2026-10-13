package solver

// The answer from the WCS file. With --crpix-center, the reference
// pixel is the center of the frame, so CRVAL1 and CRVAL2 are the
// frame's center. The CD matrix maps pixels to degrees on the sky: its
// determinant gives the scale and the parity, and its terms give the
// rotation the way astrometry.net computes it (tan_get_orientation in
// util/sip.c).

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/fits"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

// answerOf reads the solution from a WCS header.
func answerOf(h fits.Header) (SolveAnswer, error) {
	var values [6]float64
	for i, key := range []string{"CRVAL1", "CRVAL2", "CD1_1", "CD1_2", "CD2_1", "CD2_2"} {
		value, err := h.Float(key)
		if err != nil {
			return SolveAnswer{}, fmt.Errorf("the WCS file: %w", err)
		}
		values[i] = value
	}
	ra, dec, cd11, cd12, cd21, cd22 := values[0], values[1], values[2], values[3], values[4], values[5]
	det := cd11*cd22 - cd12*cd21
	// A determinant below zero is the image as the sky shows it, with
	// east to the left of north, which astrometry.net calls positive.
	parity, sign := ParityPositive, -1.0
	if det >= 0 {
		parity, sign = ParityNegative, 1.0
	}
	rotation := -math.Atan2(sign*cd21-cd12, sign*cd11+cd22) * 180 / math.Pi
	return SolveAnswer{
		Solved:   true,
		Center:   &sky.Position{RA: ra, Dec: dec},
		Scale:    math.Sqrt(math.Abs(det)) * 3600,
		Rotation: math.Mod(rotation+360, 360),
		Parity:   parity,
		Index:    indexOf(h),
	}, nil
}

// indexOf reads the series that solved the field from the engine's
// comment "index id: 4207", and answers 0 when the header holds none.
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
