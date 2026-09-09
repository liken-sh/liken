package main

// streamrows.go holds the streams table: one row per stream of one media
// file, keyed by the library, the file's path, and the stream's ordinal in
// the container. A file's streams travel with the file. They have no key
// space of their own in the seen table, and the file sweeps remove them.

import (
	"context"
	"strconv"
	"strings"
)

// streamRow is one stream of one media file, in the columns the streams
// table holds.
type streamRow struct {
	Library string
	Path    string
	Ordinal int
	Kind    string
	Codec   string
	Profile string
	// The picture columns, which an audio or a subtitle stream leaves at zero.
	Width          int
	Height         int
	Depth          int
	FrameRate      string
	ColorPrimaries string
	ColorTransfer  string
	ColorSpace     string
	DolbyVision    bool
	// The sound columns, which a video or a subtitle stream leaves at zero.
	Channels   int
	Layout     string
	SampleRate int
	Bitrate    int64
	Language   string
	Title      string
	Default    bool
	Forced     bool
	Present    bool
}

// streamKey names one row of the streams table within one library.
type streamKey struct {
	Path    string
	Ordinal int
}

// UpsertStreams writes the streams of every file the rows name, and drops
// the ordinals a file carried beyond the count of the fresh set. The drop
// runs in the same batch, before the writes, so a file that lost a stream
// never keeps the stale row.
func (c *Catalog) UpsertStreams(ctx context.Context, rows []streamRow) (int, error) {
	counts := map[streamFile]int{}
	var files []streamFile
	for _, row := range rows {
		file := streamFile{library: row.Library, path: row.Path}
		if _, held := counts[file]; !held {
			files = append(files, file)
		}
		counts[file]++
	}

	statements := make([]statement, 0, len(files)+len(rows))
	for _, file := range files {
		statements = append(statements, statement{
			sql:    `DELETE FROM streams WHERE library = ? AND path = ? AND ordinal >= ?`,
			params: []any{file.library, file.path, counts[file]},
		})
	}
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `INSERT INTO streams (library, path, ordinal, kind, codec, profile, width, height, depth, ` +
				`frame_rate, color_primaries, color_transfer, color_space, dolby_vision, channels, layout, ` +
				`sample_rate, bitrate, language, title, is_default, forced, present) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, path, ordinal) DO UPDATE SET ` +
				`kind = excluded.kind, codec = excluded.codec, profile = excluded.profile, ` +
				`width = excluded.width, height = excluded.height, depth = excluded.depth, ` +
				`frame_rate = excluded.frame_rate, color_primaries = excluded.color_primaries, ` +
				`color_transfer = excluded.color_transfer, color_space = excluded.color_space, ` +
				`dolby_vision = excluded.dolby_vision, channels = excluded.channels, layout = excluded.layout, ` +
				`sample_rate = excluded.sample_rate, bitrate = excluded.bitrate, language = excluded.language, ` +
				`title = excluded.title, is_default = excluded.is_default, forced = excluded.forced, ` +
				`present = excluded.present`,
			params: []any{row.Library, row.Path, row.Ordinal, row.Kind, row.Codec, row.Profile,
				row.Width, row.Height, row.Depth, row.FrameRate, row.ColorPrimaries, row.ColorTransfer,
				row.ColorSpace, flag(row.DolbyVision), row.Channels, row.Layout, row.SampleRate,
				row.Bitrate, row.Language, row.Title, flag(row.Default), flag(row.Forced), flag(row.Present)},
		})
	}
	return c.apply(ctx, statements)
}

// The two columns that name one file.
type streamFile struct {
	library string
	path    string
}

// DeleteStreams removes stream rows by their whole primary key.
func (c *Catalog) DeleteStreams(ctx context.Context, library string, keys []streamKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM streams WHERE library = ? AND path = ? AND ordinal = ?`,
			params: []any{library, key.Path, key.Ordinal},
		}
	}
	return c.apply(ctx, statements)
}

// DeleteStreamsOfFiles removes every stream of each file the sweep found,
// so a stream never outlives the file it belongs to.
func (c *Catalog) DeleteStreamsOfFiles(ctx context.Context, library string, paths []string) (int, error) {
	statements := make([]statement, len(paths))
	for i, path := range paths {
		statements[i] = statement{
			sql:    `DELETE FROM streams WHERE library = ? AND path = ?`,
			params: []any{library, path},
		}
	}
	return c.apply(ctx, statements)
}

// streamKeys splits each composite key the sweep read into the path and the
// ordinal the delete names.
func streamKeys(keys []string) []streamKey {
	out := make([]streamKey, len(keys))
	for i, key := range keys {
		path, ordinal, _ := strings.Cut(key, linkKeySeparator)
		number, _ := strconv.Atoi(ordinal)
		out[i] = streamKey{Path: path, Ordinal: number}
	}
	return out
}

// One bounded batch of one library's stream keys, with the two key columns
// joined the way every sweep of a composite key joins them.
func librarySweepStreamSQL() string {
	return `SELECT path || char(31) || ordinal FROM streams WHERE library = ? LIMIT ?`
}
