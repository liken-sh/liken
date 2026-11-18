//! The list of changes as the pages show it: the newest few entries in
//! the sidebar, and every entry of the last day on `/changed`.

use askama::Template;
use k8s_openapi::jiff::Timestamp;

use super::{Change, WINDOW_HOURS, for_sidebar};
use transect_observe::health::times;

/// The entries to render, and the time of the render, which decides
/// whether an entry's time shows its weekday.
#[derive(Debug, Default, PartialEq)]
pub struct Listed {
    pub now: Timestamp,
    pub changes: Vec<Change>,
    /// The number of entries in the whole list.  In the sidebar,
    /// `changes` has only some of them.
    pub total: usize,
}

impl Listed {
    /// The entries of `all` that the sidebar shows.
    pub fn sidebar(all: &[Change], now: Timestamp) -> Self {
        Self {
            now,
            changes: for_sidebar(all),
            total: all.len(),
        }
    }

    pub fn whole(all: Vec<Change>, now: Timestamp) -> Self {
        Self {
            now,
            total: all.len(),
            changes: all,
        }
    }

    pub fn time(&self, change: &Change) -> String {
        change.time(self.now)
    }

    /// "All 37 changes in the last 24 hours →", under the sidebar's
    /// entries.
    pub fn all_text(&self) -> String {
        format!(
            "All {} in the last {WINDOW_HOURS} hours →",
            times(self.total as i64, "change")
        )
    }

    /// The line at the top of `/changed`.
    pub fn summary(&self) -> String {
        format!(
            "{} in the last {WINDOW_HOURS} hours, newest first, in UTC.",
            times(self.total as i64, "change")
        )
    }
}

/// The sources that the list is read from, which the page shows under
/// the summary.
pub const SOURCES: &str = "transect keeps no history, so each entry is a time an object still records: a ReplicaSet's or a ControllerRevision's creation, a Job's, a CronJob's last run, the fetch of the revision a GitOps applier applied, a container's last exit, a machine condition's last change, and an alert's start. A change whose object is gone leaves no entry.";

#[derive(Template)]
#[template(path = "changed/page.html", blocks = ["main"])]
pub struct ChangedPage {
    pub listed: Listed,
}

impl ChangedPage {
    pub fn events_url(&self) -> &'static str {
        "/events/changed"
    }

    pub fn sources(&self) -> &'static str {
        SOURCES
    }
}
