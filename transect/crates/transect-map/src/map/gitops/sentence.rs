//! The words a GitOps object's page shows.  Everything else on it is a
//! name, a URL, a revision, or the tool's own message.

use super::GitOpsPage;
use transect_supply::gitops::Ready;

impl GitOpsPage {
    /// Its kind and name, as the card's head.
    pub fn title(&self) -> String {
        transect_graph::edges::kind_and_name(&self.detail.object.kind, &self.detail.object.name)
    }

    /// Whether its tool reports it ready, in a few words.
    pub fn ready_text(&self) -> &'static str {
        match self.detail.ready {
            Ready::True => "ready",
            Ready::False(_) => "not ready",
            Ready::Unknown(_) => "reconciling",
            Ready::Missing => "not reconciled yet",
        }
    }

    /// The dot beside it, filled for a failure as a workload's is.
    pub fn dot_class(&self) -> &'static str {
        match self.detail.ready {
            Ready::True => "dot-clear",
            Ready::False(_) => "concern-failing",
            Ready::Unknown(_) => "concern-warning",
            Ready::Missing => "concern-notice",
        }
    }

    /// The tool's message, when it is not ready.
    pub fn message(&self) -> Option<&str> {
        match &self.detail.ready {
            Ready::False(message) | Ready::Unknown(message) if !message.is_empty() => Some(message),
            _ => None,
        }
    }

    /// What the revision line is called: what an applier applied, or
    /// what a source fetched.
    pub fn revision_label(&self) -> &'static str {
        if self.is_source() {
            "Fetched"
        } else {
            "Applied"
        }
    }

    /// The heading over what reconciles from it: what an applier
    /// applies, or what reconciles from a source.
    pub fn reconciling_heading(&self) -> &'static str {
        if self.is_source() {
            "Reconciles from it"
        } else {
            "Applies"
        }
    }

    /// The text shown in place of the list when nothing transect reads
    /// reconciles from it.
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
