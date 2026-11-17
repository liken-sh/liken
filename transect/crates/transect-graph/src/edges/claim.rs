//! A `PersistentVolumeClaim` by namespace and name.  Storage follows a
//! claim to its volume, and metrics read a claim's used bytes.

/// A claim, by namespace and name.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Claim {
    pub namespace: String,
    pub name: String,
}

impl Claim {
    pub fn new(namespace: &str, name: &str) -> Self {
        Self {
            namespace: namespace.into(),
            name: name.into(),
        }
    }
}
