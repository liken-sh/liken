//! What the workload page writes on and around its pod tiles and its
//! volumes column: each tile's line, its bars' titles and figures, and
//! the pages its tiles and claims open.

use std::collections::{HashMap, HashSet};

use super::super::bar;
use super::WorkloadPage;
use super::sentence;
use super::tiles::PodTile;
use transect_graph::edges::{Object, Plurals};
use transect_ground::storage::Mounts;

impl WorkloadPage {
    pub fn tile_line(&self, tile: &PodTile) -> String {
        sentence::tile_line(tile)
    }

    pub fn memory_title(&self, tile: &PodTile) -> String {
        bar::memory_title(&tile.memory)
    }

    pub fn cpu_title(&self, tile: &PodTile) -> String {
        bar::cpu_title(&tile.cpu)
    }

    pub fn memory_unscaled(&self, tile: &PodTile) -> Option<String> {
        bar::memory_unscaled(&tile.memory)
    }

    pub fn cpu_unscaled(&self, tile: &PodTile) -> Option<String> {
        bar::cpu_unscaled(&tile.cpu)
    }

    /// Where a tile leads: its pod's page.
    pub fn tile_href(&self, tile: &PodTile) -> String {
        format!("/namespaces/{}/pods/{}", self.namespace, tile.name)
    }

    /// Where a mounted claim's name leads: its page, when the cluster
    /// has it.
    pub fn claim_href(&self, claim: &str) -> Option<String> {
        self.claim_pages.get(claim).cloned()
    }
}

/// The page of each claim in `mounts` that `held` contains, by its name.
pub fn claim_pages(
    mounts: &Mounts,
    namespace: &str,
    held: &HashSet<Object>,
    plurals: &Plurals,
) -> HashMap<String, String> {
    let claims = mounts.groups.iter().flat_map(|group| &group.mounts);
    claims
        .filter_map(|mount| {
            let object = Object::new("", "PersistentVolumeClaim", Some(namespace), &mount.claim);
            let page = held
                .contains(&object)
                .then(|| transect_graph::links::page(&object, plurals));
            Some((mount.claim.clone(), page.flatten()?))
        })
        .collect()
}
