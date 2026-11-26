// One video stream of a file that a play request names, with its frame and
// the active area the probe measured for it. A film stored with its
// letterbox bars in the frame has black rows that mpv draws as picture, so
// the film's display reads the area to put a subtitle cue of two or more
// lines in those rows.

/// One video stream. `stream` is the stream's index in the container, the
/// index ffprobe reports and mpv calls `ff-index`. `width` and `height` are
/// the size of its frame, in which `active_area` is measured. A stream whose
/// samples did not agree has no area.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Video {
    pub stream: i64,
    pub width: i64,
    pub height: i64,
    pub active_area: Option<ActiveArea>,
}

/// The rectangle of a frame that contains picture, in pixels of the frame:
/// its first column and row, and its size.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ActiveArea {
    pub left: i64,
    pub top: i64,
    pub width: i64,
    pub height: i64,
}
