//! The text of a GitOps object's page. All other text on it is a name, a
//! URL, a revision, or a message from the GitOps tool.

use super::GitOpsPage;
use transect_supply::gitops::Ready;

impl GitOpsPage {
    /// The kind and name, as the card's heading.
    pub fn title(&self) -> String {
        transect_graph::edges::kind_and_name(&self.detail.object.kind, &self.detail.object.name)
    }

    /// A few words for whether the GitOps tool reports the object ready.
    pub fn ready_text(&self) -> &'static str {
        match self.detail.ready {
            Ready::True => "ready",
            Ready::False(_) => "not ready",
            Ready::Unknown(_) => "reconciling",
            Ready::Missing => "not reconciled yet",
        }
    }

    /// The CSS class of the dot beside the ready text.
    pub fn dot_class(&self) -> &'static str {
        match self.detail.ready {
            Ready::True => "dot-clear",
            Ready::False(_) => "concern-failing",
            Ready::Unknown(_) => "concern-warning",
            Ready::Missing => "concern-notice",
        }
    }

    /// The tool's message, when the object is not ready.
    pub fn message(&self) -> Option<&str> {
        match &self.detail.ready {
            Ready::False(message) | Ready::Unknown(message) if !message.is_empty() => Some(message),
            _ => None,
        }
    }

    /// The label of the revision line: "Applied" for an applier,
    /// "Fetched" for a source.
    pub fn revision_label(&self) -> &'static str {
        if self.is_source() {
            "Fetched"
        } else {
            "Applied"
        }
    }

    /// The heading over the list of related objects: what an applier
    /// applies, or what reconciles from a source.
    pub fn reconciling_heading(&self) -> &'static str {
        if self.is_source() {
            "Reconciles from it"
        } else {
            "Applies"
        }
    }

    /// The text shown in place of the list when that list is empty.
    pub fn nothing_text(&self) -> &'static str {
        if self.is_source() {
            "Nothing transect reads reconciles from it."
        } else {
            "Nothing transect reads was applied by it."
        }
    }

    fn is_source(&self) -> bool {
        !self.detail.applies
    }
}
