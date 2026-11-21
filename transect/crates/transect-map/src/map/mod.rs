//! The view models and templates of the map's pages, from the cluster
//! down to a pod.

mod bar;
mod cluster;
mod edge_row;
mod gitops;
mod gone;
pub mod ground;
pub mod known;
mod machine;
mod namespace;
pub mod pages;
mod pod;
pub mod relation;
mod section;
mod sidebar;
pub mod vulnerabilities;
mod workload;

use askama::Template;

pub use cluster::{ClusterMap, Selected, UNSORTED};
pub use edge_row::{EdgeRow, ON_THE_MAP};
pub use gitops::GitOpsPage;
pub use gone::{Above, GonePage, Named as Gone};
pub use known::{
    Board, Entry as KnownEntry, KnownPage, Reading, Standing, Why, chips as known_chips, every_chip,
};
pub use machine::{
    DevicesRead, Driver, MachinePage, OwnLogs, Reads as MachineReads, Underneath, VolumeLine,
    by_driver, legend as machine_legend, runs_here, switches as machine_switches,
};
pub use namespace::{Contents, NamespacePage};
pub use pod::{Contents as PodContents, PodPage};
pub use section::{Contents as SectionContents, SectionPage, drawing as section_drawing};
pub use sidebar::Sidebar;
use transect_frame::frame::crumbs::Crumb;
pub use workload::{
    Contents as WorkloadContents, WorkloadPage, card as workload_card, controller, find, has_page,
    known_through,
};

use crate::cards::NamespaceCard;
use transect_ground::loss;
use transect_ground::machines::{self, Machine};
use transect_ground::storage::Ground;

/// The cluster map page. Its `main` block, with the map and the sidebar,
/// also renders alone, as the fragment that a pushed update replaces.
#[derive(Template)]
#[template(path = "map/cluster.html", blocks = ["main"])]
pub struct ClusterPage {
    pub map: ClusterMap,
    pub selected: Option<Selected>,
    pub machines: Vec<Machine>,
    /// Why the machines band shows no usage bars, when it shows none.
    pub usage_note: Option<transect_resources::resources::OffLine>,
    /// Why no machine strip lists a device, when the `holds` edge kind is
    /// off.
    pub devices_note: Option<&'static str>,
    pub ground: Ground,
    pub sidebar: Sidebar,
    /// The selected namespace's card. The page shows it instead of the
    /// sidebar.
    pub card: Option<NamespaceCard>,
    /// What losing a set of machines takes down. The page shows it in
    /// place of the sidebar.
    pub loss: Option<loss::Sidebar>,
    pub edge_row: EdgeRow,
}

impl ClusterPage {
    pub fn machine_groups(&self) -> Vec<machines::Group<'_>> {
        machines::groups(&self.machines)
    }

    /// The breadcrumb: the cluster, then the predicted loss when there is
    /// one, or else the selected namespace's layer, then the selected
    /// namespace. The cluster always links to `/`, even on the map
    /// itself, so that clicking it clears the selection and the loss.
    pub fn crumbs(&self) -> Vec<Crumb> {
        match (&self.selected, &self.loss) {
            (Some(selected), Some(loss)) if !loss.all_real() => vec![
                Crumb::home(),
                Crumb::link(&loss.crumb(), format!("/?lose={}", loss.param())),
                Crumb::current_name(&selected.namespace),
            ],
            (Some(selected), _) => vec![
                Crumb::home(),
                Crumb::link(&selected.layer, format!("/#{}", selected.layer)),
                Crumb::current_name(&selected.namespace),
            ],
            (None, Some(loss)) if !loss.all_real() => {
                vec![Crumb::home(), Crumb::current(&loss.crumb())]
            }
            (None, _) => vec![Crumb::home()],
        }
    }

    /// Whether the card lists the namespace's workloads as chips. It
    /// does, because the namespace's box on the map shows only their
    /// count.
    pub fn card_workloads(&self) -> bool {
        true
    }

    pub fn card_lens(&self) -> bool {
        true
    }

    pub fn card_link(&self) -> Option<String> {
        self.selected
            .as_ref()
            .map(|selected| format!("/namespaces/{}", selected.namespace))
    }

    /// The URL of the event stream for this page's view. It is built from
    /// the selection and the loss, not from the raw query string, so a
    /// link to a namespace or machine that no longer exists streams the
    /// plain cluster.
    pub fn events_url(&self) -> String {
        match (&self.selected, &self.loss) {
            (Some(selected), Some(loss)) if !loss.all_real() => format!(
                "/events/?lose={}&select={}",
                loss.param(),
                selected.namespace
            ),
            (Some(selected), _) => format!("/events/?select={}", selected.namespace),
            (None, Some(loss)) if !loss.all_real() => {
                format!("/events/?lose={}", loss.param())
            }
            (None, _) => "/events/".into(),
        }
    }

    /// Whether the page shows something other than `/`, so that Escape
    /// and a click on empty map leave it. A loss of only the machines
    /// that are already lost predicts nothing, so it is the same view as
    /// `/`.
    pub fn away(&self) -> bool {
        let lost = self.loss.as_ref().is_some_and(|loss| !loss.all_real());
        self.selected.is_some() || lost
    }

    /// Where Escape goes: to the loss view when a namespace is selected
    /// inside it, and to `/` otherwise.
    pub fn away_href(&self) -> String {
        match (&self.selected, &self.loss) {
            (Some(_), Some(loss)) if !loss.all_real() => format!("/?lose={}", loss.param()),
            _ => "/".into(),
        }
    }
}
