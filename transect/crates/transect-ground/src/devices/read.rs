use super::{Devices, Off};
use transect_cluster::cluster::Cluster;
use transect_cluster::cluster::discovery::{self, State};

impl Devices {
    /// Builds `Devices` from the `ResourceClaim`s and `ResourceSlice`s in
    /// `cluster`'s stores. When API discovery has not finished, or found
    /// that the cluster does not serve these kinds or that transect may
    /// not list them, the result is `Devices::off` with the reason.
    pub fn read(cluster: &Cluster) -> Self {
        let (claims, slices) = match cluster.device_objects() {
            State::On(objects) => objects,
            State::Probing | State::Retrying => return Devices::off(Off::Retrying),
            State::Off(discovery::Off::NotServed) => return Devices::off(Off::NotServed),
            State::Off(discovery::Off::Forbidden) => return Devices::off(Off::Forbidden),
        };
        Devices::build(
            claims.iter().map(AsRef::as_ref),
            slices.iter().map(AsRef::as_ref),
        )
    }
}
