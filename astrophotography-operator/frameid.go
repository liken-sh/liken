package main

// Each frame's ID is a UUIDv7 in the frame's FRAMEID keyword. Before
// each exposure the runner writes the keyword to the driver's
// FITS_HEADER property, so the driver puts it in the file it saves. The
// catalog uses the ID as the frame's key. A UUIDv7 starts with its
// creation time, so the IDs sort in the order the frames were taken.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// newFrameID returns a UUIDv7 (RFC 9562, section 5.7): 48 bits of Unix
// time in milliseconds, the version 7, the variant 10, and 74 random
// bits.
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
