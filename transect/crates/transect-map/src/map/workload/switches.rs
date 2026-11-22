//! The links to the namespace's other workloads, in the row above a
//! workload page's main row.

use transect_cluster::ownership;
use transect_graph::edges::Object;
use transect_observe::health::Level;

use super::Contents;

/// A link to one of the namespace's workloads, in the row above the
/// page's main row.
#[derive(Debug, PartialEq)]
pub struct Switch {
    /// The workload's name, followed by its replica count, as in
    /// "web ×3", when the count is not one.
    pub label: String,
    pub href: String,
    /// Whether this is the workload that the page shows.
    pub current: bool,
    pub level: Level,
}

impl Switch {
    /// The CSS classes of the switch.  The current workload is outlined
    /// in the selection color of the cluster map, and a workload at level
    /// `Warning` or worse has a border in the warning color.
    pub fn class(&self) -> String {
        let mut class = String::from("switch");
        if self.current {
            class.push_str(" switch-current");
        }
        if self.level >= Level::Warning {
            class.push_str(" switch-warning");
        }
        class
    }
}

/// A switch for each workload that has a page.  The built-in kinds come
/// first and then the custom kinds, each sorted by name, so a workload
/// keeps its place in the row as the reader moves between pages.
pub(super) fn switches(current: &Object, contents: &Contents) -> Vec<Switch> {
    let mut switches: Vec<(bool, &str, Switch)> = contents
        .owners
        .iter()
        .filter_map(|&owner| {
            let href = transect_graph::links::page(owner, contents.plurals)?;
            let replicas = contents.workloads.get(owner).replicas();
            let label = match replicas {
                Some(replicas) if replicas != 1 => format!("{} ×{replicas}", owner.name),
                _ => owner.name.clone(),
            };
            let custom = ownership::custom(owner);
            let level = contents.by_workload.get(owner).map(|h| h.level);
            let switch = Switch {
                label,
                href,
                current: owner == current,
                level: level.unwrap_or_default(),
            };
            Some((custom, owner.name.as_str(), switch))
        })
        .collect();
    switches.sort_by(|a, b| (a.0, a.1).cmp(&(b.0, b.1)));
    switches.into_iter().map(|(_, _, switch)| switch).collect()
}
