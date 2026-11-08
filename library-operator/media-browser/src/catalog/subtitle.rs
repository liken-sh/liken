// One subtitle track of a file that a play request names, with the event
// count the probe read for it. `media-operator` chooses the subtitle track
// by its role, and a forced track often carries no flag or title that marks
// it, only far fewer events than the full track in its language. mpv cannot
// read that count, so the browser sends it.

/// Which track an entry names. An embedded track is named by its index in
/// the container, the index ffprobe reports and mpv calls `ff-index`. An
/// external track is named by the base name of its file beside the video.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Track {
    Stream(i64),
    File(String),
}

/// One subtitle track and how many events it holds. No count is not a
/// count of zero: a track of zero events is empty, and a track with no
/// count is one the probe could not count.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Subtitle {
    pub track: Track,
    pub events: Option<i64>,
}
