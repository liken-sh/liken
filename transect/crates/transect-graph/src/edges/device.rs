//! A device that a driver publishes in a `ResourceSlice`, and the words
//! a page shows it as.

/// One device, by the driver that publishes it, the pool it is in, and
/// its name in that pool, which together name it across the cluster.
/// The machine is the `nodeName` of the device or of the slice that
/// lists it, or, when no slice lists it, the one machine its claim's
/// allocation selects.  It is none for a device that a slice offers to
/// more than one machine, through `allNodes` or a node selector.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Device {
    pub driver: String,
    pub pool: String,
    pub name: String,
    pub machine: Option<String>,
}

impl Device {
    /// The device by its driver's first label and its own name, as "gpu
    /// card-0" for `card-0` from `gpu.example.com`.  A driver's name is
    /// a domain, and its first label is the part that differs between
    /// one vendor's drivers.
    pub fn short(&self) -> String {
        let driver = self.driver.split('.').next().unwrap_or(&self.driver);
        format!("{driver} {}", self.name)
    }

    /// Every part that names it, for a title: "gpu.example.com/alpha/card-0".
    pub fn full(&self) -> String {
        format!("{}/{}/{}", self.driver, self.pool, self.name)
    }
}
