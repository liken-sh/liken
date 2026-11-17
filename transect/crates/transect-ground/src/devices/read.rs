//! Reads the devices from the cluster's stores as they are now.

use super::{Devices, Off};
use transect_cluster::cluster::Cluster;
use transect_cluster::cluster::discovery::{Off as Answer, State};

impl Devices {
    /// The devices of every allocated claim in `cluster` as of now, or
    /// the reason that transect cannot read the holds edges.
    pub fn read(cluster: &Cluster) -> Self {
        let (claims, slices) = match cluster.device_objects() {
            State::On(objects) => objects,
            State::Probing | State::Retrying => return Devices::off(Off::Retrying),
            State::Off(Answer::NotServed) => return Devices::off(Off::NotServed),
            State::Off(Answer::Forbidden) => return Devices::off(Off::Forbidden),
        };
        Devices::build(
            claims.iter().map(AsRef::as_ref),
            slices.iter().map(AsRef::as_ref),
        )
    }
}
