//! Evaluates how traffic reaches each workload: `NetworkPolicy`
//! rules, the entry path of each workload through its `Service` and
//! `Ingress` objects, and the objects that an address or name in one
//! object's fields refers to.

pub mod addresses;
pub mod entry;
pub mod policy;
