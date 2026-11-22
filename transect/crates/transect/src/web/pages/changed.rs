//! The `/changed` page.

use k8s_openapi::jiff::Timestamp;

use super::super::model::Model;
use transect_supply::changed::{ChangedPage, Listed};

/// Every change of the last day, newest first.
pub fn changed_page(model: &Model, now: Timestamp) -> ChangedPage {
    let firing = &model.minute(now).firing;
    let changes = model.recorded.arranged(firing, now);
    ChangedPage {
        listed: Listed::whole(changes, now),
    }
}
