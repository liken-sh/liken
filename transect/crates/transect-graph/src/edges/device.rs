//! A device that a driver publishes in a `ResourceSlice`, and the text a
//! page shows for it.

/// One device. Its driver, pool, and name together identify it across
/// the cluster. `machine` is the `nodeName` of the device or of the
/// slice that lists it. When no slice lists the device, it is the one
/// machine that its claim's allocation selects. It is none for a device
/// that a slice offers to more than one machine, through `allNodes` or a
/// node selector.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Device {
    pub driver: String,
    pub pool: String,
    pub name: String,
    pub machine: Option<String>,
}

impl Device {
    /// The first label of the driver's name and the device's name, as
    /// "gpu card-0" for `card-0` from `gpu.example.com`. A driver's name
    /// is a domain, and its first label tells one of a vendor's drivers
    /// from another.
    pub fn short(&self) -> String {
        let driver = self.driver.split('.').next().unwrap_or(&self.driver);
        format!("{driver} {}", self.name)
    }

    /// The driver, pool, and name, for a title:
    /// "gpu.example.com/alpha/card-0".
    pub fn full(&self) -> String {
        format!("{}/{}/{}", self.driver, self.pool, self.name)
    }
}
