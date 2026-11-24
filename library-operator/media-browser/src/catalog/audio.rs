// One audio stream of a file that a play request names, with the format
// facts the probe read for it. mpv reports the profile and the channel
// layout only of the audio track that it decodes, so the film's display
// cannot name the format of the other tracks from mpv. The browser sends
// the facts of every audio stream, and the display names each track with
// them.

/// One audio stream, in the fields of ffprobe that the probe recorded.
/// `stream` is the stream's index in the container, the index ffprobe
/// reports and mpv calls `ff-index`. An empty `profile` or `layout` is one
/// that ffprobe did not report.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Audio {
    pub stream: i64,
    pub codec: String,
    pub profile: String,
    pub channels: i64,
    pub layout: String,
}
