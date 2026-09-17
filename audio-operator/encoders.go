package main

// The one or two processes a tap runs.
//
// pw-record reads the graph and prints raw samples on stdout. For
// FLAC or Ogg Opus one encoder reads those samples and prints its own
// container. WAV runs no encoder, because the samples are already
// what the data chunk carries, and wav.go writes the 44 bytes in
// front of them.
//
// A sink tap differs from a source tap by one property.
// stream.capture.sink is PipeWire's own key, "try to capture the sink
// output instead of source output" (pw_keys), and WirePlumber's
// src/scripts/lib/common-utils.lua reads it to link the stream to the
// sink's monitor ports. pw-record has no flag for it, so -P puts it
// in the stream properties. A source tap is the same line without the
// property.
//
// Both encoders write stdout through stdio, so a silent sink would
// deliver nothing until the tap ended. The closure carries coreutils'
// libstdbuf.so, and each encoder runs with LD_PRELOAD naming it and
// _STDBUF_O=0, which is what stdbuf -o0 does.

import (
	"os"
	"strconv"
	"strings"
)

// stdbufLibrary is where coreutils keeps libstdbuf.so in the closure.
// The release gate asserts this path by name, because no daemon maps
// it and the map check would report nothing if it fell out of the
// closure.
const stdbufLibrary = "/usr/libexec/coreutils/libstdbuf.so"

// stdbufVariable lets a deployment name another path for the library,
// for the reason every other setting here is an environment variable.
const stdbufVariable = "CAPTURE_STDBUF"

// flacMD5Warning is the stderr line flac prints on every tap. The MD5
// sum of the samples goes in STREAMINFO, which flac rewrites at the
// end of a file it can seek, and stdout has no seek. The line is
// expected and is not a failure.
const flacMD5Warning = "WARNING, cannot write back MD5 sum when encoding to stdout"

// recordCommand is the tap itself.
//
// --raw with the trailing dash is what makes pw-record write samples
// to stdout. --format s16 is the width every player reads. The rate
// and channel count come from the graph, so audioconvert resamples
// nothing in the running case.
func recordCommand(node, stream string, direction pwDirection, format captureFormat) []string {
	return []string{
		"pw-record",
		"-P", streamProperties(stream, direction),
		"--target", node,
		"--raw",
		"--format", "s16",
		"--rate", strconv.Itoa(format.Rate),
		"--channels", strconv.Itoa(format.Channels),
		"-",
	}
}

// streamProperties is the SPA JSON object -P carries: the name this
// tap's own node takes in the graph, and, for a sink, PipeWire's key
// for reading the monitor ports.
//
// A sink's monitor ports carry what the sink receives, and spec.mute
// is applied after them, so a muted Sink taps at the level it was
// sent and muting a speaker changes nothing on this route. A Source
// is the other way round: its mute is in front of the ports a tap
// reads, so a muted microphone taps as silence. Both were measured on
// liken-1.
//
// The name is how the confirmation finds this tap among every other
// client of the socket. PipeWire sets no application.process.id here,
// because the kernel cannot translate a peer's pid across PID
// namespaces, which is the same reason config/51-access-rules.conf
// marks every client of this socket flatpak. A name this container
// chose is a property that is certainly there and certainly unique.
func streamProperties(stream string, direction pwDirection) string {
	properties := `node.name = "` + stream + `"`
	if direction == directionSink {
		properties += `, stream.capture.sink = true`
	}
	return "{ " + properties + " }"
}

// streamName names one tap's node in the graph. The request id is
// already unique for the life of the request, and no endpoint name
// enters it, so a person reading pw-dump sees which request a stream
// belongs to and nothing about what was captured.
func streamName(requestID string) string {
	return captureAudience + "-" + requestID
}

// encoderCommand is the process the raw samples are piped into, or
// nothing for WAV.
//
// For FLAC, the STREAMINFO total-samples field stays zero, which RFC
// 9639 defines as unknown. For Ogg Opus, a request with no bitrate
// knob leaves the rate to opusenc(1): "The default for input with a
// sample rate of 44.1 kHz or higher is 64 kbit/s per mono stream and
// 96 kbit/s per coupled pair."
func encoderCommand(form representation, format captureFormat, bitrate int) []string {
	switch form.Extension {
	case "flac":
		return []string{
			"flac",
			"--force-raw-format",
			"--endian=little",
			"--sign=signed",
			"--bps=16",
			"--channels=" + strconv.Itoa(format.Channels),
			"--sample-rate=" + strconv.Itoa(format.Rate),
			"--stdout",
			"-",
		}
	case "opus":
		command := []string{
			"opusenc",
			"--raw",
			"--raw-bits", "16",
			"--raw-rate", strconv.Itoa(format.Rate),
			"--raw-chan", strconv.Itoa(format.Channels),
		}
		if bitrate > 0 {
			command = append(command, "--bitrate", strconv.Itoa(bitrate))
		}
		return append(command, "-", "-")
	default:
		return nil
	}
}

// encoderEnvironment is the process environment an encoder runs with:
// the pod's own, plus the two variables that turn stdio's block
// buffering on stdout into no buffering at all.
//
// The two are replaced rather than appended. glibc reads the first
// LD_PRELOAD in the environment, so a pod that already sets one would
// keep it and the encoder would buffer.
func encoderEnvironment() []string {
	library := os.Getenv(stdbufVariable)
	if library == "" {
		library = stdbufLibrary
	}
	held := []string{"LD_PRELOAD=" + library, "_STDBUF_O=0"}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "LD_PRELOAD" || name == "_STDBUF_O" {
			continue
		}
		held = append(held, entry)
	}
	return held
}

// expectedStderr says whether a line an encoder printed is one this
// container expects.
//
// What decides whether a tap failed is the exit status, not stderr:
// both encoders write a banner and a progress bar there and exit zero
// on every successful tap. This filter is for the three failures that
// happen before a process is running, where its stderr is the whole
// story and flac's own warning would only be noise in it.
func expectedStderr(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	return strings.Contains(trimmed, flacMD5Warning)
}

// unexpectedStderr is what a start failure carries: the lines a process
// printed that this container does not expect, in the process's own
// words. A tap that ran and then ended reports its exit status and one
// line instead; tapExit holds that.
func unexpectedStderr(output string) string {
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		if expectedStderr(line) {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	return strings.Join(kept, "; ")
}
