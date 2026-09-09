package main

// probes.go is the probe ledger as the walk reads it. The probe fact writes
// one record per file into .liken/probe.yaml. The walk reads that record
// into the technical columns of the file row and into one stream row per
// stream. The walk never writes the ledger, because the scan Job mounts the
// volume read-only.

import "math"

// The records of one ledger, keyed by the entry path the probe fact wrote
// each of them under.
type folderProbes map[string]probedFile

// One ledger per folder, read on the first request and held after that.
// Two passes of one scan read the same folder, and the volume is a network
// mount, so the second pass must not read the file again.
type probeLedgers struct {
	kind string
	held map[string]folderProbes
}

func newProbeLedgers(kind string) *probeLedgers {
	return &probeLedgers{kind: kind, held: map[string]folderProbes{}}
}

// The records that name the files of one directory. For a movie's extras
// folder, they are the title folder's records. A ledger that cannot be read
// is an error: the caller marks the pass incomplete, and the folder reads as
// one with no records for that pass.
func (l *probeLedgers) of(dir string) (folderProbes, error) {
	folder := likenFolderOf(l.kind, dir)
	if probes, read := l.held[folder]; read {
		return probes, nil
	}
	ledger, err := readLikenLedger(folder, factProbe)
	probes := folderProbes{}
	for _, record := range ledger.Probes {
		probes[record.Path] = record
	}
	l.held[folder] = probes
	return probes, err
}

// fill writes the technical columns of one file row from the record the
// ledger holds for it, and returns one stream row per stream. A file with no
// record keeps what the sidecar and the name gave it.
//
// A record whose modified stamp is not the file's own describes an earlier
// file at that path. It fills only the probed column. The probe gap compares
// probed with modified and reads the difference as work to do.
func (p folderProbes) fill(row *fileRow, entry string) []streamRow {
	record, held := p[entry]
	if !held {
		return nil
	}
	row.Probed = record.Modified
	if record.Modified != row.Modified {
		return nil
	}
	row.Bitrate = record.Bitrate
	row.DurationMs = probeMilliseconds(record.Duration)
	video := record.first(fileTypeVideo)
	row.VideoCodec, row.Width, row.Height = video.Codec, video.Width, video.Height
	row.AudioCodec = record.first(fileTypeAudio).Codec
	rows := make([]streamRow, len(record.Streams))
	for ordinal, stream := range record.Streams {
		rows[ordinal] = stream.row(row.Library, row.Path, ordinal)
	}
	return rows
}

// The first stream of one kind, or an empty stream when the record holds
// none of that kind.
func (p probedFile) first(kind string) probedStream {
	for _, stream := range p.Streams {
		if stream.Kind == kind {
			return stream
		}
	}
	return probedStream{}
}

// One stream as the catalog holds it, at its own position in the container.
func (s probedStream) row(library, path string, ordinal int) streamRow {
	return streamRow{
		Library:        library,
		Path:           path,
		Ordinal:        ordinal,
		Kind:           s.Kind,
		Codec:          s.Codec,
		Profile:        s.Profile,
		Width:          s.Width,
		Height:         s.Height,
		Depth:          s.Depth,
		FrameRate:      s.FrameRate,
		ColorPrimaries: s.Color.Primaries,
		ColorTransfer:  s.Color.Transfer,
		ColorSpace:     s.Color.Space,
		DolbyVision:    s.DolbyVision,
		Channels:       s.Channels,
		Layout:         s.Layout,
		SampleRate:     s.SampleRate,
		Bitrate:        s.Bitrate,
		Language:       s.Language,
		Title:          s.Title,
		Default:        s.Default,
		Forced:         s.Forced,
		Present:        true,
	}
}

// A duration ffprobe gives in seconds, in the milliseconds the catalog holds.
func probeMilliseconds(duration float64) int64 {
	return int64(math.Round(duration * 1000))
}
