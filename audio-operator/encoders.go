package main

// A capture runs pw-record, and for FLAC or Ogg Opus it also runs an
// encoder. pw-record reads the PipeWire graph and writes raw samples to
// stdout. The encoder reads those samples on stdin and writes the
// finished container to stdout. WAV needs no encoder, because the raw
// samples are the data chunk, and wav.go writes the 44-byte header in
// front of them.
//
// A sink tap sets the PipeWire stream property stream.capture.sink
// (pw_keys: "try to capture the sink output instead of source output"),
// and a source tap does not. WirePlumber reads the property in
// src/scripts/lib/common-utils.lua and links the stream to the sink's
// monitor ports. pw-record has no flag for it, so the command sets it
// through -P.
//
// flac and opusenc write to stdout through stdio, which buffers a pipe
// in blocks, so a tap of a silent sink would deliver no bytes until it
// ended. Each encoder runs with LD_PRELOAD set to coreutils'
// libstdbuf.so and _STDBUF_O=0, which is what stdbuf -o0 does.

import (
	"os"
	"strconv"
	"strings"
)

// stdbufLibrary is the path of libstdbuf.so in the image. The smoke
// test checks that the file exists, because no running daemon maps the
// library, and nothing else would fail if the image lost it.
const stdbufLibrary = "/usr/libexec/coreutils/libstdbuf.so"

// stdbufVariable overrides stdbufLibrary.
const stdbufVariable = "CAPTURE_STDBUF"

// flacMD5Warning is the line that flac writes to stderr on every tap,
// including a tap that succeeds. flac stores the MD5 sum of the samples
// in the STREAMINFO block at the start of the file, and it fills that
// in by seeking back after the last sample. A pipe cannot seek, so flac
// warns and leaves the field empty.
const flacMD5Warning = "WARNING, cannot write back MD5 sum when encoding to stdout"

// recordCommand returns the pw-record command line for one tap. --raw
// with the trailing "-" makes pw-record write samples to stdout. The
// samples are 16-bit signed, which every player reads. The caller passes
// the endpoint's own rate and channel count, so PipeWire does not
// convert them.
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

// streamProperties returns the SPA JSON object that -P passes to
// pw-record: the node.name of the tap's stream, and stream.capture.sink
// for a sink tap.
//
// A sink's monitor ports carry the audio before spec.mute applies, so
// the tap of a muted Sink still records what was sent to it. A source's
// mute applies before its output ports, so the tap of a muted Source
// records silence.
//
// confirmLink finds the tap's stream by its node.name. Nothing else
// identifies it: PipeWire sets no application.process.id on the clients
// of this socket, because the kernel cannot translate a peer's pid
// across PID namespaces, and every tap has the same application.name.
// The same limit is why config/51-access-rules.conf marks every client
// of this socket as flatpak.
func streamProperties(stream string, direction pwDirection) string {
	properties := `node.name = "` + stream + `"`
	if direction == directionSink {
		properties += `, stream.capture.sink = true`
	}
	return "{ " + properties + " }"
}

// streamName returns the node.name of a tap's stream. The request id
// makes it unique. It leaves out the endpoint's name, so pw-dump shows
// which request a stream belongs to and not what it records.
func streamName(requestID string) string {
	return captureAudience + "-" + requestID
}

// encoderCommand returns the encoder command line that reads the raw
// samples on stdin, or nil for WAV.
//
// A pipe has no known length, so flac leaves the total-samples field of
// STREAMINFO at zero, which RFC 9639 defines as unknown. When bitrate is
// zero, opusenc uses its own default. Its manual page says: "The
// default for input with a sample rate of 44.1 kHz or higher is 64
// kbit/s per mono stream and 96 kbit/s per coupled pair."
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

// encoderEnvironment returns the operator's environment with
// LD_PRELOAD and _STDBUF_O set so that the encoder's stdout is
// unbuffered. It drops any value of either variable that the pod set,
// because the dynamic loader reads the first LD_PRELOAD in the
// environment and would ignore a second one.
func encoderEnvironment() []string {
	library := os.Getenv(stdbufVariable)
	if library == "" {
		library = stdbufLibrary
	}
	environment := []string{"LD_PRELOAD=" + library, "_STDBUF_O=0"}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "LD_PRELOAD" || name == "_STDBUF_O" {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

// expectedStderr reports whether a line is blank or is flac's MD5
// warning.
//
// Both encoders print a banner and a progress bar to stderr when they
// succeed, so the exit status, not stderr, shows whether a tap failed.
// This filter is for a command that failed to start or exited with an
// error. Its stderr is then the only record of the cause, and the
// warning would hide that record.
func expectedStderr(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	return strings.Contains(trimmed, flacMD5Warning)
}

// unexpectedStderr returns the lines of a process's stderr that
// expectedStderr rejects, joined with "; ", for the error of a command
// that failed. A tap that ran and then ended reports a tapExit instead.
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
