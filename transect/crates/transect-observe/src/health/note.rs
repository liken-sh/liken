//! A reason's note as HTML.

use crate::alerts::markup;

use super::Reason;

impl Reason {
    /// The `note` as HTML. An alert's note is its summary, which can
    /// contain inline Markdown, so it is rendered. Every other note is
    /// plain text and is escaped.
    pub fn note_html(&self) -> Option<String> {
        let note = self.note()?;
        Some(match self {
            Self::Alert { .. } => markup::inline(&note),
            _ => markup::escape(&note),
        })
    }
}
