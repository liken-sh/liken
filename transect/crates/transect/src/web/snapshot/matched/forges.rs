//! The conditions of a `Forge` that serves no repository `transect`
//! reads: no GitOps source fetches from a host it names, and no running
//! image's source label is on one.

use std::collections::HashSet;

use super::super::Snapshot;
use transect_resources::resources::{Condition, DocId};
use transect_supply::forge::{Forges, web};
use transect_supply::images::Lookup;

impl Snapshot {
    pub(super) fn unmatched_forges(&self, forges: &Forges) -> Vec<(DocId, Condition)> {
        let mut hosts: HashSet<String> = HashSet::new();
        for (_, url) in self.gitops.sources() {
            hosts.extend(web(url).map(|web| web.host.to_string()));
        }
        for source in self.image_sources() {
            hosts.extend(web(&source).map(|web| web.host.to_string()));
        }
        let served: HashSet<&str> = hosts
            .iter()
            .filter_map(|host| forges.of(host))
            .map(|forge| forge.name.as_str())
            .collect();
        let unserved = self.resources.forges.iter();
        let unserved = unserved.filter(|forge| !served.contains(forge.name.as_str()));
        unserved
            .map(|forge| {
                let message = "no repository transect reads, from a GitOps source or an image's source label, is on a host it names or serves";
                (forge.doc, Condition::unmatched(message))
            })
            .collect()
    }

    /// The source label of every running container's image whose
    /// labels `transect` has read.
    fn image_sources(&self) -> Vec<String> {
        let statuses = self.pods.iter().filter_map(|pod| pod.status.as_ref());
        let statuses = statuses.flat_map(|status| {
            let containers = status.container_statuses.iter().flatten();
            containers.chain(status.init_container_statuses.iter().flatten())
        });
        let images = statuses.filter_map(|status| transect_supply::images::parse(&status.image_id));
        images
            .filter_map(|image| match self.images.lookup(&image) {
                Lookup::Read(provenance) => provenance.source.clone(),
                _ => None,
            })
            .collect()
    }
}
