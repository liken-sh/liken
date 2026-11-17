//! A workload's access tab: its two drawings, with its card beside
//! them.

use askama::Template;

use super::Drawing;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_map::cards::WorkloadCard;

#[derive(Template)]
#[template(path = "access/drawing/access.html", blocks = ["main"])]
pub struct AccessPage {
    pub namespace: String,
    pub layer: String,
    pub name: String,
    /// The workload's own page.
    pub workload_path: String,
    pub reach: Drawing,
    pub change: Drawing,
    pub card: WorkloadCard,
}

impl AccessPage {
    pub fn path(&self) -> String {
        format!("{}/access", self.workload_path)
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(&self.namespace, format!("/namespaces/{}", self.namespace)),
            Crumb::link_name(&self.name, self.workload_path.clone()),
            Crumb::current("access"),
        ]
    }

    pub fn tabs(&self) -> Tabs {
        let tabs = Tabs::workload(&self.workload_path, Lens::Access, true);
        match self.card.vulnerabilities.is_some() {
            true => tabs.with_vulnerabilities(&self.workload_path, false),
            false => tabs,
        }
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path())
    }
}
