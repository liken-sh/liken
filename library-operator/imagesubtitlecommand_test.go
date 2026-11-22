package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standInFfprobe puts a shell script named ffprobe alone on the path, so the
// window read's one call that starts a process is proved without a media
// file in the repository.
func standInFfprobe(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\n"+script)
	if err := os.Chmod(filepath.Join(dir, "ffprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return filepath.Join(dir, "a.mkv")
}

const twoPacketAnswer = `{ "packets": [ { "stream_index": 3, "pts_time": "1.000000", "data": "\n00000000: 80  .\n" }, { "stream_index": 3, "pts_time": "2.000000", "data": "\n00000000: 80  .\n" } ] }`

// A window that ffprobe reads to its end gives every packet and no error.
// The read once ended ffprobe's context before it collected the exit, and
// the command then reported a canceled context for a read that was whole.
func TestAWindowThatFfprobeFinishesGivesEveryPacket(t *testing.T) {
	path := standInFfprobe(t, "printf '%s\\n' '"+twoPacketAnswer+"'\n")

	var times []string
	err := ffprobeSubtitleWindow(t.Context(), path, 150, func(packet subtitlePacket) bool {
		times = append(times, packet.Time)
		return true
	})

	if err != nil {
		t.Fatalf("the read returned %v, want no error", err)
	}
	if got := strings.Join(times, " "); got != "1.000000 2.000000" {
		t.Errorf("the packets are at %q, want both", got)
	}
}

// When the probe has the cues it needs, it ends an ffprobe that is still
// reading, and the read is not an error.
func TestAWindowThatTheProbeStopsEndsFfprobe(t *testing.T) {
	path := standInFfprobe(t, "printf '%s\\n' '"+twoPacketAnswer+"'\nwhile :; do :; done\n")

	packets := 0
	err := ffprobeSubtitleWindow(t.Context(), path, 150, func(subtitlePacket) bool {
		packets++
		return false
	})

	if err != nil {
		t.Fatalf("the read returned %v, want no error", err)
	}
	if packets != 1 {
		t.Errorf("the probe took %d packets, want the read to stop after 1", packets)
	}
}

// The error carries what ffprobe wrote to stderr, as the header read's does.
func TestAWindowReadThatFailsIsAnError(t *testing.T) {
	path := standInFfprobe(t, "echo 'moov atom not found' >&2\nexit 1\n")

	err := ffprobeSubtitleWindow(t.Context(), path, 150, func(subtitlePacket) bool { return true })

	if err == nil || !strings.Contains(err.Error(), "moov atom not found") {
		t.Errorf("the read returned %v, want ffprobe's own words in the error", err)
	}
}
