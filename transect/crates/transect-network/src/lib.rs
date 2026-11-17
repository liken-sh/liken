//! How traffic reaches each workload.  This crate evaluates each
//! `NetworkPolicy`, builds the entry of each workload from its
//! Services and Ingresses, and resolves the addresses that
//! one object names of another.

pub mod addresses;
pub mod entry;
pub mod policy;
