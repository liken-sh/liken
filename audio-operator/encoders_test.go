package main

import (
	"slices"
	"strings"
	"testing"
)

func TestASinkTapCarriesPipeWiresOwnCaptureProperty(t *testing.T) {
	got := recordCommand("liken-1-usb-0573-1573-a34004801402-usb-audio", "audio-capture-0f1b2c3d",
		directionSink, captureFormat{Rate: 48000, Channels: 2})
	want := []string{
		"pw-record",
		"-P", `{ node.name = "audio-capture-0f1b2c3d", stream.capture.sink = true }`,
		"--target", "liken-1-usb-0573-1573-a34004801402-usb-audio",
		"--raw",
		"--format", "s16",
		"--rate", "48000",
		"--channels", "2",
		"-",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the tap is %v, want %v", got, want)
	}
	// pw-record has no --monitor flag, so the property links the stream
	// to the sink's monitor ports.
	if slices.Contains(got, "--monitor") {
		t.Error("the tap passes --monitor, want no flag that pw-record does not have")
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
			t.Errorf("a source tap passes %q, want no stream.capture.sink, which would link it to a monitor", entry)
		}
	}
}

// confirmLink finds a tap's stream by the node.name that the command
// gives it, so every tap needs a name and no two taps may share one.
func TestEveryTapNamesItsOwnStream(t *testing.T) {
	for _, direction := range []pwDirection{directionSink, directionSource} {
		command := recordCommand("kitchen", streamName("0f1b2c3d"), direction,
			captureFormat{Rate: 48000, Channels: 2})
		properties := command[2]
		if !strings.Contains(properties, `node.name = "audio-capture-0f1b2c3d"`) {
			t.Errorf("a %s tap passes %q, want node.name = \"audio-capture-0f1b2c3d\"", direction, properties)
		}
		// One -P passes both properties as one SPA JSON object, the form
		// that the pw-cat manual documents. Two -P flags would depend on
		// pw-cat merging them.
		if repeated := slices.Contains(command[3:], "-P"); repeated {
			t.Errorf("a %s tap passes -P more than once: %v, want one -P", direction, command)
		}
	}
	if streamName("a") == streamName("b") {
		t.Errorf("streamName returned %q for two requests, want two names", streamName("a"))
	}
	if strings.Contains(streamName("0f1b2c3d"), "kitchen") {
		t.Errorf("streamName = %q, want no endpoint name in it", streamName("0f1b2c3d"))
	}
}

func TestWAVRunsNoEncoder(t *testing.T) {
	wav, _ := representationFor("wav")
	if command := encoderCommand(wav, captureFormat{Rate: 48000, Channels: 2}, 0); command != nil {
		t.Errorf("encoderCommand for WAV = %v, want no encoder", command)
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

func TestTheEncoderSetsABitrateOnlyWhenTheQueryGivesOne(t *testing.T) {
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
		t.Errorf("encoderEnvironment = %v, want LD_PRELOAD=/usr/lib/coreutils/libstdbuf.so", encoderEnvironment())
	}
}

func TestFlacsMD5WarningIsNotAFailure(t *testing.T) {
	output := "\n" + flacMD5Warning + "\n"
	if got := unexpectedStderr(output); got != "" {
		t.Errorf("unexpectedStderr = %q for the MD5 warning, want the empty string", got)
	}
	if !expectedStderr("  " + flacMD5Warning) {
		t.Error("expectedStderr = false for the MD5 warning, want true")
	}
}

func TestARealFailureKeepsTheProcessesOwnWords(t *testing.T) {
	output := flacMD5Warning + "\nERROR: unsupported number of channels 9\n"
	got := unexpectedStderr(output)
	if got != "ERROR: unsupported number of channels 9" {
		t.Errorf("unexpectedStderr = %q, want the encoder's own line %q", got, "ERROR: unsupported number of channels 9")
	}
}
