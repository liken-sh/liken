package main

// probe.go is the probe concern: the one container that opens a video file.
// The answer goes into the .nfo and not into the catalog alone, because the
// volume holds the truth. A rebuilt catalog reads the sidecar and probes
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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// One file's bound, so a file the kernel will not answer for cannot hold the
// container open.
var ffprobeTimeout = time.Minute

// One read of one file's container, which a test replaces with an answer of
// its own.
type mediaProbe func(ctx context.Context, path string) ([]byte, error)

// The role's whole program. A failure is a non-zero exit, so the Job fails
// and Kubernetes retries it.
func runProbe() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	work := newEnricher(os.Stdout)
	if err := work.probeGap(stopped, ffprobeFile); err != nil {
		work.logf("the probe container failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// A catalog read that fails ends the container, because the gap list is the
// work and there is nothing to do without it. A file that will not open
// records an error attempt, and the run carries on to the next file.
func (e *enricher) probeGap(ctx context.Context, probe mediaProbe) error {
	if err := e.markRunStarted(ctx); err != nil {
		return err
	}
	paths, err := e.gaps(ctx, concernProbe, time.Now().UTC())
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
	e.logf("probed %d of the %d files with no stream details", probed, len(paths))
	return nil
}

// One file: read it, write its stream details into the sidecar the scanner
// reads them from, and record what happened either way.
func (e *enricher) probeOne(ctx context.Context, probe mediaProbe, path string) {
	absolute := filepath.Join(e.root, path)
	result := attemptFound
	if err := e.writeStreamDetails(ctx, probe, absolute); err != nil {
		e.logf("could not probe %s: %v", path, err)
		result = attemptError
	}
	folder, entry := likenFolderFor(e.kind, absolute)
	e.recordAttempt(folder, concernProbe, entry, result, time.Now().UTC())
}

// The answer is one surgical edit of the sidecar, so every other element the
// sidecar holds stays as it was. A file with no sidecar gets a minimal one,
// and the later concerns edit that same file.
func (e *enricher) writeStreamDetails(ctx context.Context, probe mediaProbe, absolute string) error {
	output, err := probe(ctx, absolute)
	if err != nil {
		return err
	}
	var read ffprobeAnswer
	if err := json.Unmarshal(output, &read); err != nil {
		return fmt.Errorf("reading the probe of %s: %w", absolute, err)
	}
	element, err := xml.MarshalIndent(read.fileInfo(), "  ", "  ")
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
// later concern edits this same file.
func minimalNFO(rootElement, title string) []byte {
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(title))
	return fmt.Appendf(nil, "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<%s>\n  <title>%s</title>\n</%s>\n",
		rootElement, escaped.String(), rootElement)
}

// The ffprobe answer this container reads: the container's own facts and one
// entry per stream.
type ffprobeAnswer struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
}

type ffprobeStream struct {
	CodecType string      `json:"codec_type"`
	CodecName string      `json:"codec_name"`
	Width     int         `json:"width"`
	Height    int         `json:"height"`
	Channels  int         `json:"channels"`
	Duration  string      `json:"duration"`
	Tags      ffprobeTags `json:"tags"`
}

type ffprobeTags struct {
	Language string `json:"language"`
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
	Width    int    `xml:"width"`
	Height   int    `xml:"height"`
	Duration int    `xml:"durationinseconds"`
}

type nfoAudioElement struct {
	Codec    string `xml:"codec"`
	Channels int    `xml:"channels,omitempty"`
	Language string `xml:"language,omitempty"`
}

type nfoSubtitleElement struct {
	Language string `xml:"language,omitempty"`
}

// The container's duration wins over a stream's, because it is the length of
// the file as a player sees it. Every video, audio, and subtitle stream is
// written, not the first of each, because a second audio track is a fact a
// person looks for.
func (a ffprobeAnswer) fileInfo() nfoFileInfoElement {
	var details nfoStreamDetailsBody
	seconds := probeSeconds(a.Format.Duration)
	for _, stream := range a.Streams {
		switch stream.CodecType {
		case fileTypeVideo:
			duration := seconds
			if duration == 0 {
				duration = probeSeconds(stream.Duration)
			}
			details.Video = append(details.Video, nfoVideoElement{
				Codec: stream.CodecName, Width: stream.Width, Height: stream.Height, Duration: duration,
			})
		case fileTypeAudio:
			details.Audio = append(details.Audio, nfoAudioElement{
				Codec: stream.CodecName, Channels: stream.Channels, Language: stream.Tags.Language,
			})
		case fileTypeSubtitle:
			details.Subtitle = append(details.Subtitle, nfoSubtitleElement{Language: stream.Tags.Language})
		}
	}
	return nfoFileInfoElement{StreamDetails: details}
}

// A duration ffprobe states as a decimal string reads as whole seconds, which
// is what the sidecar carries.
func probeSeconds(value string) int {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int(seconds + 0.5)
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
// sidecar-less title is the case this concern exists for. The edit never
// rewrites the document it read. It replaces one element and keeps every
// other byte.
func (w *volumeWriter) editNFO(path, rootElement, title string, element xmlElement, replacement []byte) error {
	document, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		document = minimalNFO(rootElement, title)
	} else if err != nil {
		return err
	}
	edited, err := editElement(document, element, replacement)
	if err != nil {
		return fmt.Errorf("editing %s: %w", filepath.Base(path), err)
	}
	return w.write(path, edited)
}
