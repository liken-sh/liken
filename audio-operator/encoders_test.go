package main

import (
	"slices"
	"strings"
	"testing"
)

func TestASinkTapCarriesPipeWiresOwnCaptureProperty(t *testing.T) {
	got := recordCommand("usb-0573-1573-a34004801402-usb-audio", directionSink,
		captureFormat{Rate: 48000, Channels: 2})
	want := []string{
		"pw-record",
		"-P", "stream.capture.sink=true",
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
	got := recordCommand("desk-mic", directionSource, captureFormat{Rate: 44100, Channels: 1})
	want := []string{
		"pw-record",
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
	if slices.Contains(got, "stream.capture.sink=true") {
		t.Error("a source tap carries the sink property, which would link it to a monitor")
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
