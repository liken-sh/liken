package main

// Each frame's identity is a UUIDv7 in the keyword FRAMEID (plan 01).
// The runner writes it through FITS_HEADER before each exposure, so the
// driver writes it into the frame when it creates the file, and the
// catalog and the archive key on it. A UUIDv7 starts with the time it
// was made, so identities sort in the order the frames were taken.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// newFrameID answers a new UUIDv7 (RFC 9562, section 5.7): 48 bits of
// the Unix time in milliseconds, the version 7, the variant 10, and 74
// random bits.
func newFrameID(now time.Time) string {
	var b [16]byte
	_, _ = rand.Read(b[6:])
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(now.UnixMilli()))
	copy(b[:6], ms[2:])
	b[6] = 0x70 | b[6]&0x0f
	b[8] = 0x80 | b[8]&0x3f
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
