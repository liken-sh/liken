// The streams of one video file, as the probe recorded them, and the text
// subtitle files beside it that mpv loads. A movie's page reads them to
// show what each file holds, and to mark the audio and subtitle tracks a
// play of it takes.

use super::Subtitle;

/// One stream of a media file: one row of the catalog's `streams` table.
/// Each field holds the kind of stream it belongs to and its default on the
/// others, as the table does. The names are ffprobe's own.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Stream {
    /// The stream's index in the container, which mpv calls `ff-index`.
    pub ordinal: i64,
    /// `video`, `audio`, `subtitle`, `image`, or `data`.
    pub kind: String,
    /// ffprobe's codec name, such as `hevc`, `truehd`, or `subrip`.
    pub codec: String,
    /// ffprobe's profile, such as `Main 10` or `DTS-HD MA`.
    pub profile: String,
    pub width: i64,
    pub height: i64,
    /// Bits per sample of a video stream.
    pub depth: i64,
    /// The frame rate as ffprobe writes it, such as `24000/1001`.
    pub frame_rate: String,
    /// The transfer characteristic, such as `smpte2084` for PQ.
    pub color_transfer: String,
    pub dolby_vision: bool,
    pub channels: i64,
    /// The channel layout, such as `5.1(side)` or `stereo`.
    pub layout: String,
    pub language: String,
    pub title: String,
    pub default: bool,
    pub forced: bool,
    /// How many events a subtitle stream holds, and nothing where the
    /// probe read no count.
    pub events: Option<i64>,
}

/// What a play of one video file has to choose from: the streams in
/// container order, and the external subtitle files mpv loads beside it,
/// in name order.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Tracks {
    pub streams: Vec<Stream>,
    pub beside: Vec<Subtitle>,
}
