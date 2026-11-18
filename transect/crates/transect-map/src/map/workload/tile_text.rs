//! The text of the workload page around its pod tiles and its volumes
//! column: each tile's line, the titles and figures of its bars, and the
//! pages that its tiles and claims link to.

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

    /// The pod's page.
    pub fn tile_href(&self, tile: &PodTile) -> String {
        format!("/namespaces/{}/pods/{}", self.namespace, tile.name)
    }

    /// The link of a mounted claim's name: its page, or `None` when the
    /// claim has no page.
    pub fn claim_href(&self, claim: &str) -> Option<String> {
        self.claim_pages.get(claim).cloned()
    }
}

/// The link to the page of each claim in `mounts`, keyed by claim name.
/// A claim that is not in `with_pages` has no page and is left out.
pub fn claim_pages(
    mounts: &Mounts,
    namespace: &str,
    with_pages: &HashSet<Object>,
    plurals: &Plurals,
) -> HashMap<String, String> {
    let claims = mounts.groups.iter().flat_map(|group| &group.mounts);
    claims
        .filter_map(|mount| {
            let object = Object::new("", "PersistentVolumeClaim", Some(namespace), &mount.claim);
            let page = with_pages
                .contains(&object)
                .then(|| transect_graph::links::page(&object, plurals));
            Some((mount.claim.clone(), page.flatten()?))
        })
        .collect()
}
