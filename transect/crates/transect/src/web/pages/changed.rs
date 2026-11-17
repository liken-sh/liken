//! The `/changed` page, and the sidebar's share of the same list.

use k8s_openapi::jiff::Timestamp;

use super::super::model::Model;
use transect_graph::edges::Shown;
use transect_supply::changed::{ChangedPage, Listed};

/// Every change of the last day, newest first.
pub fn changed_page(model: &Model, shown: &Shown, now: Timestamp) -> ChangedPage {
    let firing = &model.minute(now).firing;
    let changes = model.edge_set(shown).recorded.arranged(firing, now);
    ChangedPage {
        listed: Listed::whole(changes, now),
    }
}
