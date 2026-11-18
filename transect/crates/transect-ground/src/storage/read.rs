use super::{OnMachines, Storage};
use transect_cluster::cluster::Cluster;

impl Storage {
    /// Builds `Storage` from the claims, volumes, storage classes, and
    /// attachments in `cluster`'s stores. A volume of a CSI driver that
    /// `on_machines` lists is placed on the machines that mount it.
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
