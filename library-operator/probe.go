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
	if err := e.markRunStarted(ctx); err != nil {
		return err
	}
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

// The record is the truth, so it is written first and the sidecar follows
// from it. An audio file gets a record and no sidecar, because no player
// reads an .nfo beside a music file.
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
	if fileTypeOf(absolute) != fileTypeVideo {
		return nil
	}
	return e.writeStreamDetails(absolute, record)
}

// The answer is one surgical edit of the sidecar, so every other element the
// sidecar holds stays as it was. A file with no sidecar gets a minimal one,
// and the later facts edit that same file.
func (e *enricher) writeStreamDetails(absolute string, record probedFile) error {
	element, err := xml.MarshalIndent(record.fileInfo(), "  ", "  ")
	if err != nil {
		return err
	}
	sidecar, rootElement, title := probeSidecar(e.kind, absolute)
	return e.writer.editNFO(sidecar, rootElement, title, xmlElement{name: "fileinfo"}, element)
}

// The sidecar names and the root elements the scanner reads a title's, a
// series', and an episode's facts from.
const (
	movieSidecarName  = "movie.nfo"
	seriesSidecarName = "tvshow.nfo"
	nfoRootMovie      = "movie"
	nfoRootSeries     = "tvshow"
	nfoRootEpisode    = "episodedetails"
)

// Which sidecar carries a file's stream details: the title's own for the
// first video of a movie folder, and the file's own for every other video.
// That is where the scanner reads each of them from, so a trailer's details
// never land in movie.nfo.
func probeSidecar(kind, absolute string) (string, string, string) {
	dir, name := filepath.Dir(absolute), filepath.Base(absolute)
	rootElement := nfoRootEpisode
	if kind == libraryKindMovies {
		rootElement = nfoRootMovie
		if videos, err := listVideoFiles(dir); err == nil && len(videos) > 0 && videos[0] == name &&
			extrasFolderName(filepath.Base(dir)) == "" {
			title, _ := parseReleaseName(filepath.Base(dir))
			return filepath.Join(dir, movieSidecarName), nfoRootMovie, title
		}
	}
	title, _ := parseReleaseName(name)
	return sidecarBeside(absolute), rootElement, title
}

func sidecarBeside(absolute string) string {
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
// what the sidecar carries.
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
		return nil, fmt.Errorf("ffprobe %s: %w", filepath.Base(path), err)
	}
	return output, nil
}

// An absent sidecar becomes a minimal one and not an error, because the
// sidecar-less title is the case this fact exists for. A sidecar with no
// root element, an empty file or a declaration alone, is treated the same
// way, because there is nothing in it to keep. Otherwise the edit never
// rewrites the document it read. It replaces one element and keeps every
// other byte.
func (w *volumeWriter) editNFO(path, rootElement, title string, element xmlElement, replacement []byte) error {
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
