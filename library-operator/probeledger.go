package main

// probeledger.go is the probe fact's ledger record, and the mapping of one
// ffprobe answer onto it. The record keeps ffprobe's own names for codecs
// and profiles, so hevc is always hevc and never x265 or h265, and every
// reader of the catalog sees one name per codec.

import (
	"strconv"
	"strings"
	"time"
)

// The kind of a stream that is not picture, sound, or text: a font
// attachment, a timecode track, and the like.
const probedKindData = "data"

// The side data type ffprobe reports on a stream that carries a Dolby Vision
// configuration. Dolby Vision is not a color field, so this is the only sign
// of it.
const dolbyVisionSideData = "DOVI configuration record"

// One file as the probe read it, keyed by the same entry path the attempts
// use. Modified and Size are the file's own at the time of the probe, so a
// reader can tell a record of an earlier file at the same path.
type probedFile struct {
	Path      string         `yaml:"path"`
	At        time.Time      `yaml:"at"`
	Modified  int64          `yaml:"modified"`
	Size      int64          `yaml:"size"`
	Container string         `yaml:"container"`
	Duration  float64        `yaml:"duration"`
	Bitrate   int64          `yaml:"bitrate"`
	Streams   []probedStream `yaml:"streams"`
}

// One stream of one file, in ffprobe's own order and with ffprobe's own
// names. The picture fields, the sound fields, and the text fields share one
// type, and a stream leaves the fields of the other kinds empty.
type probedStream struct {
	Kind        string      `yaml:"kind"`
	Codec       string      `yaml:"codec"`
	Profile     string      `yaml:"profile,omitempty"`
	Width       int         `yaml:"width,omitempty"`
	Height      int         `yaml:"height,omitempty"`
	Depth       int         `yaml:"depth,omitempty"`
	FrameRate   string      `yaml:"frameRate,omitempty"`
	Color       probedColor `yaml:"color,omitempty"`
	DolbyVision bool        `yaml:"dolbyVision,omitempty"`
	Channels    int         `yaml:"channels,omitempty"`
	Layout      string      `yaml:"layout,omitempty"`
	SampleRate  int         `yaml:"sampleRate,omitempty"`
	Bitrate     int64       `yaml:"bitrate,omitempty"`
	Language    string      `yaml:"language,omitempty"`
	Title       string      `yaml:"title,omitempty"`
	Default     bool        `yaml:"default,omitempty"`
	Forced      bool        `yaml:"forced,omitempty"`
}

// The three color facts of a video stream. HDR is not one field in ffprobe:
// a reader derives it from the transfer function and the primaries.
type probedColor struct {
	Primaries string `yaml:"primaries,omitempty"`
	Transfer  string `yaml:"transfer,omitempty"`
	Space     string `yaml:"space,omitempty"`
}

// The ffprobe answer this container reads: the container's own facts and one
// entry per stream.
type ffprobeAnswer struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

type ffprobeStream struct {
	CodecType        string             `json:"codec_type"`
	CodecName        string             `json:"codec_name"`
	Profile          string             `json:"profile"`
	Width            int                `json:"width"`
	Height           int                `json:"height"`
	PixelFormat      string             `json:"pix_fmt"`
	BitsPerRawSample string             `json:"bits_per_raw_sample"`
	AverageFrameRate string             `json:"avg_frame_rate"`
	RealFrameRate    string             `json:"r_frame_rate"`
	ColorPrimaries   string             `json:"color_primaries"`
	ColorTransfer    string             `json:"color_transfer"`
	ColorSpace       string             `json:"color_space"`
	Channels         int                `json:"channels"`
	ChannelLayout    string             `json:"channel_layout"`
	SampleRate       string             `json:"sample_rate"`
	BitRate          string             `json:"bit_rate"`
	Duration         string             `json:"duration"`
	Disposition      ffprobeDisposition `json:"disposition"`
	SideData         []ffprobeSideData  `json:"side_data_list"`
	Tags             ffprobeTags        `json:"tags"`
}

type ffprobeDisposition struct {
	Default     int `json:"default"`
	Forced      int `json:"forced"`
	AttachedPic int `json:"attached_pic"`
}

type ffprobeSideData struct {
	Type string `json:"side_data_type"`
}

// A Matroska file states a stream's bit rate in a BPS tag, because its
// stream headers carry none. The English-suffixed form is what some muxers
// write.
type ffprobeTags struct {
	Language   string `json:"language"`
	Title      string `json:"title"`
	BPS        string `json:"BPS"`
	BPSEnglish string `json:"BPS-eng"`
}

// The whole answer as one record. The caller fills the file's own facts:
// the path, the time, the size, and the modified stamp.
func (a ffprobeAnswer) probedFile() probedFile {
	record := probedFile{
		Duration: probeFloat(a.Format.Duration),
		Bitrate:  probeInt(a.Format.BitRate),
	}
	for _, stream := range a.Streams {
		record.Streams = append(record.Streams, stream.probed())
	}
	if record.Duration == 0 {
		record.Duration = a.videoDuration()
	}
	return record
}

// The first video stream's own duration, used where the container states
// none. Some containers carry no duration of their own.
func (a ffprobeAnswer) videoDuration() float64 {
	for _, stream := range a.Streams {
		if stream.CodecType == fileTypeVideo {
			return probeFloat(stream.Duration)
		}
	}
	return 0
}

// One stream as the record holds it. The frame rate, the color, and the
// Dolby Vision flag are picture facts, so only a video stream carries them.
func (s ffprobeStream) probed() probedStream {
	kind := s.kind()
	stream := probedStream{
		Kind:       kind,
		Codec:      s.CodecName,
		Profile:    s.Profile,
		Width:      s.Width,
		Height:     s.Height,
		Depth:      s.depth(kind),
		Channels:   s.Channels,
		Layout:     s.ChannelLayout,
		SampleRate: int(probeInt(s.SampleRate)),
		Bitrate:    s.bitrate(),
		Language:   s.Tags.Language,
		Title:      s.Tags.Title,
		Default:    s.Disposition.Default == 1,
		Forced:     s.Disposition.Forced == 1,
	}
	if kind == fileTypeVideo {
		stream.FrameRate = s.frameRate()
		stream.Color = probedColor{Primaries: s.ColorPrimaries, Transfer: s.ColorTransfer, Space: s.ColorSpace}
		stream.DolbyVision = s.dolbyVision()
	}
	return stream
}

// A video stream that ffprobe marks as an attached picture is a cover, not
// a video, so a music file with cover art has no video stream.
func (s ffprobeStream) kind() string {
	switch s.CodecType {
	case fileTypeVideo:
		if s.Disposition.AttachedPic == 1 {
			return fileTypeImage
		}
		return fileTypeVideo
	case fileTypeAudio:
		return fileTypeAudio
	case fileTypeSubtitle:
		return fileTypeSubtitle
	}
	return probedKindData
}

// The bits per sample. ffprobe states it for some codecs. Where it does not,
// the pixel format names it for a picture, and a sound stream has none.
func (s ffprobeStream) depth(kind string) int {
	if depth := int(probeInt(s.BitsPerRawSample)); depth > 0 {
		return depth
	}
	if kind != fileTypeVideo && kind != fileTypeImage {
		return 0
	}
	switch {
	case strings.Contains(s.PixelFormat, "10"):
		return 10
	case strings.Contains(s.PixelFormat, "12"):
		return 12
	}
	return 8
}

// The average frame rate, or the base frame rate where the container states
// no average. Either can be 0/0, which is no rate at all.
func (s ffprobeStream) frameRate() string {
	rate := s.AverageFrameRate
	if rate == "" || rate == "0/0" {
		rate = s.RealFrameRate
	}
	if rate == "0/0" {
		return ""
	}
	return rate
}

func (s ffprobeStream) bitrate() int64 {
	for _, value := range []string{s.BitRate, s.Tags.BPS, s.Tags.BPSEnglish} {
		if rate := probeInt(value); rate > 0 {
			return rate
		}
	}
	return 0
}

func (s ffprobeStream) dolbyVision() bool {
	for _, side := range s.SideData {
		if side.Type == dolbyVisionSideData {
			return true
		}
	}
	return false
}

// ffprobe gives every number as a string, and a value it cannot measure as
// the word N/A. Both parse functions read a bad value as zero.
func probeFloat(value string) float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

func probeInt(value string) int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}
