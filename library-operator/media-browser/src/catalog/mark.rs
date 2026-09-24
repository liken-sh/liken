// One span of a file that the catalog's marks fact holds: where a community
// database places the file's intro, recap, credits, or preview. The browser
// reads every candidate and forwards them on the play request. The display
// in `media-operator` merges them and acts on the result, so nothing here
// chooses one.

/// One candidate span, as the `marks` table holds it. The two ends are
/// milliseconds from the start of the file. An absent start is the start of
/// the file, and an absent end is the end of the file, so an absence is not
/// zero.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Mark {
    /// What the span holds: `intro`, `recap`, `credits`, `preview`, or
    /// `post-credits`.
    pub kind: String,
    /// Where the span starts, in milliseconds, or nothing for the start of
    /// the file.
    pub start: Option<i64>,
    /// Where the span ends, in milliseconds, or nothing for the end of the
    /// file.
    pub end: Option<i64>,
    /// The provider block the span came from, such as `theintrodb`.
    pub source: String,
}
