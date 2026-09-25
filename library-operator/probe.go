package main

// probe.go is the probe fact: the one container that opens a media file. It
// writes the whole answer into .liken/probe.yaml, and then writes a video's
// stream details into the .nfo from that record. The ledger is the truth,
// because the volume holds it. A rebuilt catalog reads the ledger and probes
// nothing.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// One file's bound, so a file the kernel will not answer for cannot hold the
// container open.
var ffprobeTimeout = time.Minute

// One read of one file's container, which a test replaces with an answer of
// its own.
type mediaProbe func(ctx context.Context, path string) ([]byte, error)

// The read the probe fact makes of every file in its gap. It is a variable so
// a test answers in ffprobe's place.
var probeFile mediaProbe = ffprobeFile

// The probe fact's whole run: the gap of files with no current probe record,
// each read with ffprobe.
func (e *enricher) probeFact(ctx context.Context) error {
	return e.probeGap(ctx, probeFile)
}

// A catalog read that fails ends the container, because the gap list is the
// work and there is nothing to do without it. A file that will not open
// records an error attempt, and the run carries on to the next file.
func (e *enricher) probeGap(ctx context.Context, probe mediaProbe) error {
	paths, err := e.gaps(ctx, factProbe, time.Now().UTC())
	if err != nil {
		return err
	}
	probed := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.inScope(path) {
			continue
		}
		e.probeOne(ctx, probe, path)
		probed++
	}
	e.logf("probed %d of the %d files with no current probe", probed, len(paths))
	return nil
}

// One file: read it, record what it holds, and record the attempt whatever
// the outcome.
func (e *enricher) probeOne(ctx context.Context, probe mediaProbe, path string) {
	absolute := filepath.Join(e.root, path)
	result := attemptFound
	if err := e.recordProbe(ctx, probe, absolute); err != nil {
		e.logf("could not probe %s: %v", path, err)
		result = attemptError
	}
	folder, entry := likenFolderFor(e.kind, absolute)
	e.recordAttempt(folder, factProbe, entry, result, time.Now().UTC())
}

// The ledger record is the source of truth, so the probe writes it first
// and then writes the .nfo file from it. An audio file gets a record and no
// .nfo file, because no player reads an .nfo file beside a music file.
// A video whose role is not the feature also gets a record and no .nfo file.
func (e *enricher) recordProbe(ctx context.Context, probe mediaProbe, absolute string) error {
	output, err := probe(ctx, absolute)
	if err != nil {
		return err
	}
	var read ffprobeAnswer
	if err := json.Unmarshal(output, &read); err != nil {
		return fmt.Errorf("reading the probe of %s: %w", absolute, err)
	}
	size, modified, err := statFile(absolute)
	if err != nil {
		return err
	}
	folder, entry := likenFolderFor(e.kind, absolute)
	record := read.probedFile()
	record.Path, record.At = entry, time.Now().UTC()
	record.Size, record.Modified = size, modified
	record.Container = containerFromExtension(absolute)
	err = e.writer.updateLikenLedger(folder, factProbe, func(ledger *likenLedger) {
		ledger.noteProbe(record)
	})
	if err != nil {
		return err
	}
	// Jellyfin and this scanner read an .nfo file beside a trailer, an extra,
	// a sample, or a theme as a movie. So the record is the whole answer for a
	// video that is not the feature. The walk reads the streams off the
	// ledger either way.
	if fileTypeOf(absolute) != fileTypeVideo || fileRoleAt(e.kind, absolute) != fileRolePrimary {
		return nil
	}
	return e.writeStreamDetails(absolute, record)
}

// The answer is one edit of one element in the .nfo file, so every other
// element in the file stays as it was. A video with no .nfo file gets a
// minimal one, and the later facts edit that same file.
func (e *enricher) writeStreamDetails(absolute string, record probedFile) error {
	element, err := xml.MarshalIndent(record.fileInfo(), "  ", "  ")
	if err != nil {
		return err
	}
	nfoPath, rootElement, title := probeNFO(e.kind, absolute)
	return e.writer.editNFO(nfoPath, rootElement, title, xmlElement{name: "fileinfo"}, element)
}

// The .nfo file names and the root elements that the scanner reads a
// title's, a series', and an episode's facts from.
const (
	movieNFOName   = "movie.nfo"
	seriesNFOName  = "tvshow.nfo"
	nfoRootMovie   = "movie"
	nfoRootSeries  = "tvshow"
	nfoRootEpisode = "episodedetails"
)

// Which .nfo file contains a file's stream details: the title's own for the
// first video of a movie folder, and the file's own for every other video.
// That is where the scanner reads each of them from, so a trailer's details
// never land in movie.nfo.
func probeNFO(kind, absolute string) (string, string, string) {
	dir, name := filepath.Dir(absolute), filepath.Base(absolute)
	rootElement := nfoRootEpisode
	if kind == libraryKindMovies {
		rootElement = nfoRootMovie
		if videos, err := listVideoFiles(dir); err == nil && len(videos) > 0 && videos[0] == name &&
			extrasFolderName(filepath.Base(dir)) == "" {
			title, _ := parseReleaseName(filepath.Base(dir))
			return filepath.Join(dir, movieNFOName), nfoRootMovie, title
		}
	}
	title, _ := parseReleaseName(name)
	return nfoBeside(absolute), rootElement, title
}

func nfoBeside(absolute string) string {
	return strings.TrimSuffix(absolute, filepath.Ext(absolute)) + metadataExtension
}

// The smallest document a reader accepts: the root element and a title. Every
// later fact edits this same file.
func minimalNFO(rootElement, title string) []byte {
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(title))
	return fmt.Appendf(nil, "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<%s>\n  <title>%s</title>\n</%s>\n",
		rootElement, escaped.String(), rootElement)
}

// The streamdetails block, in the shape nfo.go reads and Kodi and Jellyfin
// both write.
type nfoFileInfoElement struct {
	XMLName       xml.Name             `xml:"fileinfo"`
	StreamDetails nfoStreamDetailsBody `xml:"streamdetails"`
}

type nfoStreamDetailsBody struct {
	Video    []nfoVideoElement    `xml:"video"`
	Audio    []nfoAudioElement    `xml:"audio"`
	Subtitle []nfoSubtitleElement `xml:"subtitle"`
}

type nfoVideoElement struct {
	Codec    string `xml:"codec"`
	Aspect   string `xml:"aspect,omitempty"`
	Width    int    `xml:"width,omitempty"`
	Height   int    `xml:"height,omitempty"`
	Duration int    `xml:"durationinseconds"`
	HDRType  string `xml:"hdrtype,omitempty"`
}

type nfoAudioElement struct {
	Codec    string `xml:"codec"`
	Channels int    `xml:"channels,omitempty"`
	Language string `xml:"language,omitempty"`
}

type nfoSubtitleElement struct {
	Language string `xml:"language,omitempty"`
}

// The part of the record Kodi reads. Every video, audio, and subtitle
// stream is written, not the first of each, because a second audio track is
// a fact a person looks for. A cover and a data stream are left out, because
// Kodi reads neither.
func (p probedFile) fileInfo() nfoFileInfoElement {
	var details nfoStreamDetailsBody
	for _, stream := range p.Streams {
		switch stream.Kind {
		case fileTypeVideo:
			details.Video = append(details.Video, nfoVideoElement{
				Codec:    stream.Codec,
				Aspect:   aspectRatio(stream.Width, stream.Height),
				Width:    stream.Width,
				Height:   stream.Height,
				Duration: probeSeconds(p.Duration),
				HDRType:  stream.hdrType(),
			})
		case fileTypeAudio:
			details.Audio = append(details.Audio, nfoAudioElement{
				Codec: stream.Codec, Channels: stream.Channels, Language: stream.Language,
			})
		case fileTypeSubtitle:
			details.Subtitle = append(details.Subtitle, nfoSubtitleElement{Language: stream.Language})
		}
	}
	return nfoFileInfoElement{StreamDetails: details}
}

// The aspect in Kodi's own form: width over height as a decimal with two
// places.
func aspectRatio(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(width)/float64(height), 'f', 2, 64)
}

// Which of Kodi's three HDR words a stream gets, or none when the transfer
// function is not an HDR one. Dolby Vision wins over HDR10, because a Dolby
// Vision stream carries an HDR10 base layer.
func (s probedStream) hdrType() string {
	switch {
	case s.DolbyVision:
		return hdrDolbyVision
	case s.Color.Transfer == transferPQ:
		return hdrHDR10
	case s.Color.Transfer == transferHLG:
		return hdrHLG
	}
	return ""
}

// The three HDR words Kodi reads, and the transfer functions ffprobe names
// two of them by: SMPTE 2084 is the PQ curve of HDR10, and ARIB STD-B67 is
// HLG.
const (
	hdrDolbyVision = "dolbyvision"
	hdrHDR10       = "hdr10"
	hdrHLG         = "hlg"
	transferPQ     = "smpte2084"
	transferHLG    = "arib-std-b67"
)

// A duration ffprobe states as a decimal reads as whole seconds, which is
// what the .nfo file contains.
func probeSeconds(duration float64) int {
	return int(duration + 0.5)
}

// The one call that opens a file. The timeout is per file, so one file that
// hangs costs its own minute and no more.
func ffprobeFile(ctx context.Context, path string) ([]byte, error) {
	timed, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()

	command := exec.CommandContext(timed, "ffprobe",
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w%s", filepath.Base(path), err, commandStderr(err))
	}
	return output, nil
}

// What a failed command wrote to stderr, as a suffix for its error, because
// an exit status alone says nothing about why the file would not open. The
// answer is empty for an error that carries no output.
func commandStderr(err error) string {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return ""
	}
	if words := strings.TrimSpace(string(exit.Stderr)); words != "" {
		return ": " + words
	}
	return ""
}

// Where no .nfo file exists, the edit writes a minimal one and returns no
// error, because this fact exists for the title with no .nfo file. An .nfo file
// with no root element, an empty file or a declaration alone, is treated the
// same way, because there is nothing in it to keep. Otherwise the edit never
// rewrites the document it read. It replaces one element and keeps every other
// byte.
//
// The read and the write are one step under the file's lock, because the
// probe, identity, and nfo containers edit one .nfo file at the same time.
func (w *volumeWriter) editNFO(path, rootElement, title string, element xmlElement, replacement []byte) error {
	release, err := lockNFO(w.locks, path)
	if err != nil {
		return err
	}
	defer release()
	document, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !hasRootElement(document) {
		document = minimalNFO(rootElement, title)
	}
	edited, err := editElement(document, element, replacement)
	if err != nil {
		return fmt.Errorf("editing %s: %w", filepath.Base(path), err)
	}
	return w.write(path, edited)
}
