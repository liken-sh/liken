package main

// A sink's headroom is how many samples PipeWire's ALSA node keeps
// queued on the card beyond the one quantum it writes ahead
// (api.alsa.headroom in spa/plugins/alsa/alsa-pcm.c). The node wakes
// on a timer and computes how full the card's buffer is from the
// position that the driver reports. When that position or the card's
// clock is off by more than the samples still queued, the card runs out
// and the node logs an underrun. With PCM that is a gap of one quantum.
// With a bitstream it breaks the IEC 61937 framing, and the receiver
// mutes until it finds the stream again.
//
// PipeWire's default is no headroom. Every stream on the sink plays
// later by the headroom, so the operator adds it only to a sink whose
// Sink asks for it in spec.headroom.samples, for a card that underruns
// without it. The value is in samples at the rate the sink runs: 4096
// samples is 21 ms of a bitstream at 192 kHz and 85 ms of PCM at
// 48 kHz.
//
// The node reads the headroom only when PipeWire creates it, so the
// headroom is part of the layout (layout.go). A change waits until no
// stream plays and restarts PipeWire, as a change of the positions
// does (layoutdrift.go).

import (
	"fmt"
	"strconv"
)

const headroomProperty = "api.alsa.headroom"

// headroomArgs returns no property for a sink with no headroom, so its
// node has PipeWire's default.
func headroomArgs(layout channelLayout) map[string]string {
	if layout.Headroom <= 0 {
		return nil
	}
	return map[string]string{headroomProperty: fmt.Sprint(layout.Headroom)}
}

// declaredHeadroom returns the headroom that headroomArgs wrote into a
// node's args, and 0 when it wrote none.
func declaredHeadroom(args map[string]string) int {
	samples, err := strconv.Atoi(args[headroomProperty])
	if err != nil {
		return 0
	}
	return samples
}
