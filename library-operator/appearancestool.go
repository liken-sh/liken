package main

// appearancestool.go runs the appearances tool, the Rust binary in
// appearances/ that the appearances image carries. The tool has two passes.
// detect opens the video, finds and embeds the faces in its keyframes, and
// writes the detections record beside it. match embeds the cast's headshots
// and prints one JSON document that names each face. The worker owns the
// ledger, so the tool writes no attempt, and every failure of the tool comes
// back here with the tool's own words.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The binary, a variable so a test runs a stand-in in its place, and the
// environment variable the image sets to the directory of the two models.
var appearancesBinary = "/appearances"

const appearancesModelsVariable = "APPEARANCES_MODELS"

// How long each pass may run. detect reads the keyframes of a whole feature,
// which takes minutes for a 4K file decoded in software, and the timeout
// bounds a decode that hangs. match embeds a few dozen headshots and compares
// vectors, which takes seconds.
const (
	appearancesDetectTimeout = 2 * time.Hour
	appearancesMatchTimeout  = 15 * time.Minute
)

// The most of the tool's stderr an attempt keeps. The tool's error is its last
// line, and the lines before it say what it did, so the end of the text is
// the part to keep.
const appearancesReasonLimit = 4096

// The format the first line of a detections record names, and the format of
// the match document. The worker reads only these versions.
const (
	detectionsFormat = "liken.sh/appearances/detections/v1"
	matchesFormat    = "liken.sh/appearances/matches/v1"
)

// One run of the tool that failed: the pass, the exit, and the tool's stderr
// word for word. The error's text ends with the last line of stderr, which is
// the tool's own error.
type appearancesFailure struct {
	pass   string
	err    error
	stderr string
}

func (f *appearancesFailure) Error() string {
	text := strings.TrimSpace(f.stderr)
	if at := strings.LastIndexByte(text, '\n'); at >= 0 {
		text = text[at+1:]
	}
	return fmt.Sprintf("appearances %s: %v: %s", f.pass, f.err, text)
}

// The text an attempt records for a failure: the tool's stderr, or the error
// itself where the tool did not run or ran and said nothing wrong.
func appearancesReason(err error) string {
	var failure *appearancesFailure
	if !errors.As(err, &failure) || strings.TrimSpace(failure.stderr) == "" {
		return err.Error()
	}
	text := strings.TrimSpace(failure.stderr)
	if len(text) > appearancesReasonLimit {
		text = text[len(text)-appearancesReasonLimit:]
	}
	return text
}

// One run of the tool, its standard output, and a failure that carries its
// stderr.
func runAppearances(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(timed, appearancesBinary, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, &appearancesFailure{pass: args[0], err: err, stderr: stderr.String()}
	}
	return stdout.Bytes(), nil
}

// The arguments of detect. --device auto runs the models on an Intel GPU
// where OpenVINO finds one, which it does only in a pod that holds the
// render claim. Two decode threads bound ffmpeg's memory, because ffmpeg
// otherwise starts a thread per core of the node, and each thread holds its
// own frames. With a render node, ffmpeg decodes and scales on it, and the
// cache keeps the GPU kernels OpenVINO compiles at its first start.
func detectArgs(video string, hardware bool) []string {
	args := []string{"detect", "--device", "auto", "--decode-threads", "2"}
	if hardware {
		args = append(args, "--hwaccel", "vaapi")
	}
	if renderNode() != "" {
		args = append(args, "--cache", appearancesScratch)
	}
	return append(args, video)
}

// The arguments of match. The match runs on the CPU: it embeds a few dozen
// headshots, which takes about a second there, and a GPU would compile its
// kernels again on every match, because the tool keeps no cache for match.
// credits names the cast's file, which for an episode is the series' own.
func matchArgs(folder, credits string) []string {
	return []string{"match", "--device", "cpu", "--credits", credits, folder}
}

// The first line of a detections record: the format, the size of the file
// the faces came from, and the two models that found and embedded them.
type detectionsHeader struct {
	Format   string          `json:"format"`
	Size     int64           `json:"size"`
	Detector detectionsModel `json:"detector"`
	Embedder detectionsModel `json:"embedder"`
}

// Where detect writes the record of a video: in .liken/appearances/ beside
// it, named for the file.
func detectionsPath(video string) string {
	return filepath.Join(filepath.Dir(video), likenDirectory, "appearances", filepath.Base(video)+".jsonl")
}

// Whether the record on the volume describes this file with the models this
// image holds, so the decode can be skipped. The size is the evidence that
// the record came from this file, the rule fileidentity.go holds, and the
// hashes are the evidence that its vectors compare with a gallery this image
// embeds. A record that cannot be read is not current.
func detectionsCurrent(video string, size int64, models string) bool {
	file, err := os.Open(detectionsPath(video))
	if err != nil {
		return false
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	var header detectionsHeader
	if json.Unmarshal(line, &header) != nil || header.Format != detectionsFormat || header.Size != size {
		return false
	}
	return modelCurrent(models, header.Detector) && modelCurrent(models, header.Embedder)
}

// Whether the image holds the model a record names: a file of that name in
// the model directory, with that hash.
func modelCurrent(models string, model detectionsModel) bool {
	file, err := os.Open(filepath.Join(models, model.Name+".onnx"))
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == model.SHA256
}

// The match document, in the shape the tool's README gives it.
type matchDocument struct {
	Format    string                 `json:"format"`
	Embedder  detectionsModel        `json:"embedder"`
	Threshold float32                `json:"threshold"`
	Margin    float32                `json:"margin"`
	Gallery   []appearancesPerson    `json:"gallery"`
	Unmatched []appearancesPerson    `json:"unmatched"`
	Files     map[string]matchedFile `json:"files"`
}

// One video file's part of the document, by the file's name.
type matchedFile struct {
	Size         int64                   `json:"size"`
	Observations []appearanceObservation `json:"observations"`
}

// The ledger entry of one file out of the match's output: entry is the key
// the ledger takes, name is the file's name in the document, and size is
// the size of the file on the volume. A document with no part for the file,
// or a part of another size, holds no answer for the file on the volume.
func answerFrom(output []byte, entry, name string, size int64) (appearancesEntry, error) {
	var d matchDocument
	if err := json.Unmarshal(output, &d); err != nil {
		return appearancesEntry{}, fmt.Errorf("reading the match document: %w", err)
	}
	if d.Format != matchesFormat {
		return appearancesEntry{}, fmt.Errorf("the match document is %q, and this worker reads %s",
			d.Format, matchesFormat)
	}
	matched, held := d.Files[name]
	if !held {
		return appearancesEntry{}, errors.New("the match named no detections record of the file")
	}
	if matched.Size != size {
		return appearancesEntry{}, fmt.Errorf("the match read a detections record of another size, %d bytes against %d",
			matched.Size, size)
	}
	return appearancesEntry{
		Path: entry, Size: matched.Size, Embedder: d.Embedder, Threshold: d.Threshold, Margin: d.Margin,
		Gallery: d.Gallery, Unmatched: d.Unmatched, Observations: matched.Observations,
	}, nil
}
