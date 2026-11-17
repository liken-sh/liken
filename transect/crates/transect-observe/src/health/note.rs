//! A reason's note as HTML.

use crate::alerts::markup;

use super::Reason;

impl Reason {
    /// Returns `note` as HTML.  The function renders the inline
    /// Markdown of an alert's summary, and escapes every other note.
    pub fn note_html(&self) -> Option<String> {
        let note = self.note()?;
        Some(match self {
            Self::Alert { .. } => markup::inline(&note),
            _ => markup::escape(&note),
        })
    }
}
