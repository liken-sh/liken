package main

import (
	"slices"
	"strings"
	"testing"
)

func TestASinkTapCarriesPipeWiresOwnCaptureProperty(t *testing.T) {
	got := recordCommand("usb-0573-1573-a34004801402-usb-audio", "audio-capture-0f1b2c3d",
		directionSink, captureFormat{Rate: 48000, Channels: 2})
	want := []string{
		"pw-record",
		"-P", `{ node.name = "audio-capture-0f1b2c3d", stream.capture.sink = true }`,
		"--target", "usb-0573-1573-a34004801402-usb-audio",
		"--raw",
		"--format", "s16",
		"--rate", "48000",
		"--channels", "2",
		"-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the tap is %v, want %v", got, want)
	}
	// There is no --monitor flag on pw-record; the property is what
	// links the stream to the sink's monitor ports.
	if slices.Contains(got, "--monitor") {
		t.Error("the tap names a flag pw-record does not have")
	}
}

func TestASourceTapOmitsTheSinkProperty(t *testing.T) {
	got := recordCommand("desk-mic", "audio-capture-0f1b2c3d", directionSource,
		captureFormat{Rate: 44100, Channels: 1})
	want := []string{
		"pw-record",
		"-P", `{ node.name = "audio-capture-0f1b2c3d" }`,
		"--target", "desk-mic",
		"--raw",
		"--format", "s16",
		"--rate", "44100",
		"--channels", "1",
		"-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the tap is %v, want %v", got, want)
	}
	for _, entry := range got {
		if strings.Contains(entry, "stream.capture.sink") {
			t.Error("a source tap carries the sink property, which would link it to a monitor")
		}
	}
}

// The confirmation finds this tap's own stream by the name given here,
// so every tap has to carry one and no two taps may share it.
func TestEveryTapNamesItsOwnStream(t *testing.T) {
	for _, direction := range []pwDirection{directionSink, directionSource} {
		command := recordCommand("kitchen", streamName("0f1b2c3d"), direction,
			captureFormat{Rate: 48000, Channels: 2})
		properties := command[2]
		if !strings.Contains(properties, `node.name = "audio-capture-0f1b2c3d"`) {
			t.Errorf("a %s tap names its stream %q", direction, properties)
		}
		// One -P carries both properties as a SPA JSON object, which is
		// the form pw-cat documents. Two flags would depend on pw-cat
		// merging them.
		if count := slices.Contains(command[3:], "-P"); count {
			t.Errorf("a %s tap passes -P more than once: %v", direction, command)
		}
	}
	if streamName("a") == streamName("b") {
		t.Error("two requests share one stream name")
	}
	// The name carries no endpoint name, so a person reading pw-dump
	// learns which request a stream belongs to and nothing else.
	if strings.Contains(streamName("0f1b2c3d"), "kitchen") {
		t.Error("the stream name carries what was captured")
	}
}

func TestWAVRunsNoEncoder(t *testing.T) {
	wav, _ := representationFor("wav")
	if command := encoderCommand(wav, captureFormat{Rate: 48000, Channels: 2}, 0); command != nil {
		t.Errorf("WAV ran %v", command)
	}
}

func TestFLACTakesTheEndpointsOwnFormat(t *testing.T) {
	flac, _ := representationFor("flac")
	got := encoderCommand(flac, captureFormat{Rate: 44100, Channels: 6}, 0)
	want := []string{
		"flac",
		"--force-raw-format",
		"--endian=little",
		"--sign=signed",
		"--bps=16",
		"--channels=6",
		"--sample-rate=44100",
		"--stdout",
		"-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the encoder is %v, want %v", got, want)
	}
}

func TestOpusTakesTheBitrateOnlyWhenTheKnobGivesOne(t *testing.T) {
	opus, _ := representationFor("opus")
	format := captureFormat{Rate: 48000, Channels: 2}

	got := encoderCommand(opus, format, 0)
	want := []string{
		"opusenc",
		"--raw",
		"--raw-bits", "16",
		"--raw-rate", "48000",
		"--raw-chan", "2",
		"-", "-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the encoder is %v, want %v", got, want)
	}

	got = encoderCommand(opus, format, 128)
	want = []string{
		"opusenc",
		"--raw",
		"--raw-bits", "16",
		"--raw-rate", "48000",
		"--raw-chan", "2",
		"--bitrate", "128",
		"-", "-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the encoder is %v, want %v", got, want)
	}
}

func TestAnEncoderRunsWithStdoutUnbuffered(t *testing.T) {
	environment := encoderEnvironment()
	var preload, buffering string
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, "LD_PRELOAD="); found {
			preload = value
		}
		if value, found := strings.CutPrefix(entry, "_STDBUF_O="); found {
			buffering = value
		}
	}
	if preload != stdbufLibrary {
		t.Errorf("LD_PRELOAD is %q, want %q", preload, stdbufLibrary)
	}
	if buffering != "0" {
		t.Errorf("_STDBUF_O is %q, want 0", buffering)
	}
}

func TestADeploymentCanNameAnotherLibrary(t *testing.T) {
	t.Setenv(stdbufVariable, "/usr/lib/coreutils/libstdbuf.so")
	found := false
	for _, entry := range encoderEnvironment() {
		if entry == "LD_PRELOAD=/usr/lib/coreutils/libstdbuf.so" {
			found = true
		}
	}
	if !found {
		t.Error("the named library did not reach the encoder")
	}
}

func TestFlacsMD5WarningIsNotAFailure(t *testing.T) {
	output := "\n" + flacMD5Warning + "\n"
	if got := unexpectedStderr(output); got != "" {
		t.Errorf("the MD5 warning was read as a failure: %q", got)
	}
	if !expectedStderr("  " + flacMD5Warning) {
		t.Error("the MD5 warning was not recognised")
	}
}

func TestARealFailureKeepsTheProcessesOwnWords(t *testing.T) {
	output := flacMD5Warning + "\nERROR: unsupported number of channels 9\n"
	got := unexpectedStderr(output)
	if got != "ERROR: unsupported number of channels 9" {
		t.Errorf("the failure is %q, and it must carry the encoder's own words", got)
	}
}
