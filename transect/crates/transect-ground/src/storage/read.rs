//! Storage, read from the current contents of the cluster's stores.

use super::{OnMachines, Storage};
use transect_cluster::cluster::Cluster;

impl Storage {
    /// Every claim in `cluster` followed to its volume and backend, as
    /// of now, with the volumes of each driver that `on_machines` lists
    /// placed on the disks of the machines that mount them.
    pub fn read(cluster: &Cluster, on_machines: &OnMachines) -> Self {
        let (claims, volumes) = (cluster.claims(), cluster.volumes());
        let classes = cluster.storage_classes();
        let attachments = cluster.attachments();
        Storage::placed(
            claims.iter().map(AsRef::as_ref),
            volumes.iter().map(AsRef::as_ref),
            classes.iter().map(AsRef::as_ref),
            on_machines,
        )
        .with_attachments(attachments.iter().map(AsRef::as_ref))
    }
}
