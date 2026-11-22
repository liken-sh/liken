package main

// imagesubtitleread.go reads the packets of a file's subtitle streams in one
// window of the title, with ffprobe, for the measurement of its image
// subtitle streams. The probe image already contains ffprobe, and ffprobe
// returns the bytes of each packet, so the probe decodes them in Go and
// needs no other program. The probe reads ffprobe's answer as it arrives,
// so it can stop ffprobe as soon as it has the cues it needs.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// The length of a window. One read returns the packets of every subtitle
// stream of the file in it.
const imageWindowSeconds = 300

// One packet of a subtitle stream, as ffprobe writes it: the stream's index
// in the container, the time of the packet in seconds, and its bytes as a
// hex dump.
type subtitlePacket struct {
	Stream int    `json:"stream_index"`
	Time   string `json:"pts_time"`
	Data   string `json:"data"`
}

// One read of one window of a file's subtitle packets, which a test
// replaces with packets of its own. each receives every packet in the
// file's order, and returns false when the probe needs no more.
type subtitleWindowRead func(ctx context.Context, path string, start float64, each func(subtitlePacket) bool) error

// The window read the probe fact makes of every file with an image subtitle
// stream. It is a variable so a test answers in ffprobe's place.
var readSubtitleWindow subtitleWindowRead = ffprobeSubtitleWindow

// ffprobeSubtitleWindow runs ffprobe over one window. With only subtitle
// streams selected, ffprobe stops at the first subtitle packet after the
// window, and in a long stretch with no dialogue that packet can be minutes
// of the file away. So the read has the limit of the header read, and when
// the limit ends it, the packets that arrived count. When each returns
// false, the probe ends ffprobe.
//
// The probe ends ffprobe only when it stops the read itself. Wait returns
// the context's error for a command whose context ended before Wait saw the
// command exit, so ending the context after a window that ffprobe finished
// on its own would turn a good read into a failed one.
func ffprobeSubtitleWindow(ctx context.Context, path string, start float64, each func(subtitlePacket) bool) error {
	timed, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()
	command := exec.CommandContext(timed, "ffprobe", subtitleWindowArguments(path, start)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	answer, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	stopped, readErr := readSubtitlePackets(answer, each)
	if stopped || readErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	return subtitleWindowResult(ctx.Err(), stopped, errors.Is(timed.Err(), context.DeadlineExceeded),
		readErr, waitErr, stderr.String())
}

// subtitleWindowResult is the error of one window read, or nil. A read that
// the probe stopped and a read that the limit ended both count, with the
// packets that arrived. For any other read, ffprobe's own failure is the
// cause when ffprobe wrote one to stderr, because an answer that ffprobe cut
// short also fails to parse.
func subtitleWindowResult(parent error, stopped, limitEnded bool, readErr, waitErr error, words string) error {
	switch {
	case parent != nil:
		return parent
	case stopped || limitEnded:
		return nil
	case readErr != nil && strings.TrimSpace(words) == "":
		return fmt.Errorf("reading the packets that ffprobe wrote: %w", readErr)
	case waitErr != nil:
		return fmt.Errorf("ffprobe: %w%s", waitErr, stderrSuffix(words))
	case readErr != nil:
		return fmt.Errorf("reading the packets that ffprobe wrote: %w%s", readErr, stderrSuffix(words))
	}
	return nil
}

// What ffprobe wrote to stderr, as a suffix for its error.
func stderrSuffix(words string) string {
	if words = strings.TrimSpace(words); words != "" {
		return ": " + words
	}
	return ""
}

// The arguments of one window read. The compact JSON form writes each
// packet on one line, and the entries are the three fields that the probe
// reads.
func subtitleWindowArguments(path string, start float64) []string {
	interval := strconv.FormatFloat(start, 'f', -1, 64) + "%+" + strconv.Itoa(imageWindowSeconds)
	return []string{"-v", "error", "-print_format", "json=c=1", "-select_streams", "s",
		"-read_intervals", interval, "-show_packets", "-show_data",
		"-show_entries", "packet=stream_index,pts_time,data", path}
}

// readSubtitlePackets gives each packet of ffprobe's answer to each as it
// arrives. It is true when each stopped the read. An answer with no packet
// list is a window with no subtitle packet.
func readSubtitlePackets(answer io.Reader, each func(subtitlePacket) bool) (bool, error) {
	decoder := json.NewDecoder(answer)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if token == "packets" {
			break
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if token != json.Delim('[') {
		return false, fmt.Errorf("the packets are %v, not a list", token)
	}
	for decoder.More() {
		var packet subtitlePacket
		if err := decoder.Decode(&packet); err != nil {
			return false, err
		}
		if !each(packet) {
			return true, nil
		}
	}
	return false, nil
}

// packetBytes reads the bytes of one packet from ffprobe's dump of it. Each
// line is an offset of 8 hex digits and a colon, 16 bytes in groups of two
// up to column 50, and the bytes as text after them, which this read skips.
func packetBytes(dump string) ([]byte, error) {
	var data []byte
	for line := range strings.Lines(dump) {
		if len(line) < 10 || line[8] != ':' {
			continue
		}
		digits := strings.ReplaceAll(line[10:min(len(line), 50)], " ", "")
		decoded, err := hex.DecodeString(strings.TrimSpace(digits))
		if err != nil {
			return nil, err
		}
		data = append(data, decoded...)
	}
	return data, nil
}
